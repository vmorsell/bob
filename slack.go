package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"regexp"
	"strings"

	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
	"golang.org/x/time/rate"
)

var mentionRe = regexp.MustCompile(`<@[A-Z0-9]+>\s*`)

func NewSlackHandler(client *slack.Client, signingSecret string, llm LLM, hub *Hub, maxPerMinute float64) http.Handler {
	limiter := rate.NewLimiter(rate.Limit(maxPerMinute/60), int(maxPerMinute/60)+1)

	// Get our own bot user ID so we can identify our messages in threads.
	authResp, err := client.AuthTest()
	if err != nil {
		log.Fatalf("slack auth test failed: %v", err)
	}
	botUserID := authResp.UserID
	log.Printf("Bot user ID: %s", botUserID)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "failed to read body", http.StatusBadRequest)
			return
		}

		// Verify Slack request signature.
		sv, err := slack.NewSecretsVerifier(r.Header, signingSecret)
		if err != nil {
			http.Error(w, "failed to create verifier", http.StatusUnauthorized)
			return
		}
		if _, err := sv.Write(body); err != nil {
			http.Error(w, "failed to write body to verifier", http.StatusUnauthorized)
			return
		}
		if err := sv.Ensure(); err != nil {
			http.Error(w, "invalid signature", http.StatusUnauthorized)
			return
		}

		evt, err := slackevents.ParseEvent(json.RawMessage(body), slackevents.OptionNoVerifyToken())
		if err != nil {
			http.Error(w, "failed to parse event", http.StatusBadRequest)
			return
		}

		switch evt.Type {
		case slackevents.URLVerification:
			var challenge slackevents.ChallengeResponse
			if err := json.Unmarshal(body, &challenge); err != nil {
				http.Error(w, "failed to parse challenge", http.StatusBadRequest)
				return
			}
			w.Header().Set("Content-Type", "text/plain")
			fmt.Fprint(w, challenge.Challenge)

		case slackevents.CallbackEvent:
			innerEvent := evt.InnerEvent
			switch ev := innerEvent.Data.(type) {
			case *slackevents.AppMentionEvent:
				log.Printf("app_mention from %s in %s: %s", ev.User, ev.Channel, ev.Text)

				if !limiter.Allow() {
					log.Printf("rate limited: app_mention from %s in %s", ev.User, ev.Channel)
					go replyRateLimited(client, ev)
					return
				}

				// Respond async so Slack gets a timely 200 OK.
				go handleMention(client, llm, botUserID, hub, ev)
			}
		}
	})
}

func replyRateLimited(client *slack.Client, ev *slackevents.AppMentionEvent) {
	threadTS := ev.ThreadTimeStamp
	if threadTS == "" {
		threadTS = ev.TimeStamp
	}
	_, _, err := client.PostMessage(ev.Channel,
		slack.MsgOptionText(
			fmt.Sprintf("<@%s> I'm receiving too many requests right now. Please try again in a moment.", ev.User),
			false,
		),
		slack.MsgOptionTS(threadTS),
	)
	if err != nil {
		log.Printf("failed to post rate-limited message: %v", err)
	}
}

func handleMention(client *slack.Client, llm LLM, botUserID string, hub *Hub, ev *slackevents.AppMentionEvent) {
	// Acknowledge the mention immediately.
	if err := client.AddReaction("construction_worker", slack.ItemRef{
		Channel:   ev.Channel,
		Timestamp: ev.TimeStamp,
	}); err != nil {
		log.Printf("failed to add reaction: %v", err)
	}

	var messages []Message

	if ev.ThreadTimeStamp != "" {
		// Message is in a thread — fetch full thread for context.
		replies, _, _, err := client.GetConversationReplies(&slack.GetConversationRepliesParameters{
			ChannelID: ev.Channel,
			Timestamp: ev.ThreadTimeStamp,
		})
		if err != nil {
			log.Printf("failed to get thread replies: %v", err)
			// Fall back to just the mention text.
			messages = []Message{{Role: RoleUser, Content: stripMention(ev.Text)}}
		} else {
			messages = threadToMessages(replies, botUserID)
		}
	} else {
		messages = []Message{{Role: RoleUser, Content: stripMention(ev.Text)}}
	}

	// Determine thread timestamp for replies.
	threadTS := ev.ThreadTimeStamp
	if threadTS == "" {
		threadTS = ev.TimeStamp
	}

	// Inject Slack context and hub so tools can send notifications mid-execution.
	// jobID is generated lazily in Respond() on the first tool_use.
	ctx := WithSlackThread(context.Background(), ev.Channel, threadTS)
	ctx = WithMentionTS(ctx, ev.TimeStamp)
	ctx = WithHub(ctx, hub)

	resp, err := llm.Respond(ctx, messages)

	removeReaction(client, ev.Channel, ev.TimeStamp)

	var text string
	if err != nil {
		log.Printf("llm error: %v", err)
		text = fmt.Sprintf("<@%s> Sorry, I hit an error trying to respond. Please try again.", ev.User)
	} else if resp.IsJob && resp.PRURL != "" {
		text = fmt.Sprintf("<@%s> Done! %s", ev.User, resp.PRURL)
	} else if resp.IsJob {
		text = fmt.Sprintf("<@%s> Done!", ev.User)
	} else {
		text = fmt.Sprintf("<@%s> %s", ev.User, resp.Text)
	}

	_, _, err = client.PostMessage(ev.Channel,
		slack.MsgOptionText(text, false),
		slack.MsgOptionTS(threadTS),
	)
	if err != nil {
		log.Printf("failed to post message: %v", err)
	}
}

func removeReaction(client *slack.Client, channel, timestamp string) {
	ref := slack.ItemRef{Channel: channel, Timestamp: timestamp}
	reactions, err := client.GetReactions(ref, slack.NewGetReactionsParameters())
	if err != nil {
		log.Printf("failed to get reactions for removal: %v", err)
		return
	}
	for _, r := range reactions {
		if strings.Contains(r.Name, "construction") {
			if err := client.RemoveReaction(r.Name, ref); err != nil {
				log.Printf("failed to remove reaction %q: %v", r.Name, err)
			}
			return
		}
	}
}

func threadToMessages(replies []slack.Message, botUserID string) []Message {
	var messages []Message
	for _, msg := range replies {
		text := stripMention(msg.Text)
		if strings.TrimSpace(text) == "" {
			continue
		}
		role := RoleUser
		if msg.User == botUserID {
			role = RoleAssistant
		}
		messages = append(messages, Message{Role: role, Content: text})
	}
	return messages
}

func stripMention(text string) string {
	return strings.TrimSpace(mentionRe.ReplaceAllString(text, ""))
}
