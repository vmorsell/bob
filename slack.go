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

// approvalTexts is the set of messages that count as plan approval.
var approvalTexts = map[string]bool{
	"go":         true,
	"lgtm":       true,
	"approved":   true,
	"approve":    true,
	"ship it":    true,
	"looks good": true,
	"yes":        true,
}

func isApprovalText(text string) bool {
	return approvalTexts[strings.ToLower(strings.TrimSpace(text))]
}

func NewSlackHandler(client *slack.Client, signingSecret string, orch *Orchestrator, hub *Hub, botUserID string, approver *Approver, bobURL string, maxPerMinute float64) http.Handler {
	limiter := rate.NewLimiter(rate.Limit(maxPerMinute/60), int(maxPerMinute/60)+1)

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

				go handleMention(client, orch, botUserID, hub, approver, bobURL, ev)
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

func handleMention(client *slack.Client, orch *Orchestrator, botUserID string, hub *Hub, approver *Approver, bobURL string, ev *slackevents.AppMentionEvent) {
	// Acknowledge the mention immediately.
	if err := client.AddReaction("construction_worker", slack.ItemRef{
		Channel:   ev.Channel,
		Timestamp: ev.TimeStamp,
	}); err != nil {
		log.Printf("failed to add reaction: %v", err)
	}

	// Determine thread timestamp for replies.
	threadTS := ev.ThreadTimeStamp
	if threadTS == "" {
		threadTS = ev.TimeStamp
	}

	userText := stripMention(ev.Text)

	// Build context with Slack thread info.
	ctx := WithSlackThread(context.Background(), ev.Channel, threadTS)
	ctx = WithMentionTS(ctx, ev.TimeStamp)
	ctx = WithHub(ctx, hub)

	// Check for active job in this thread.
	activeJobID := hub.ActiveJobForThread(ev.Channel, threadTS)

	var result OrchestratorResult
	var err error

	if activeJobID != "" {
		state, hasState := hub.GetJobState(activeJobID)

		if hasState && state.Phase == PhaseAwaitingApproval && isApprovalText(userText) {
			// Text-based approval — delegate to approver.
			removeReaction(client, ev.Channel, ev.TimeStamp)
			approver.Approve(ctx, activeJobID, ev.Channel, threadTS, fmt.Sprintf("<@%s>", ev.User))
			return
		}

		// Reply to active job (question answer or plan feedback).
		// Post "Working on it..." status message.
		msg := "Working on it..."
		if bobURL != "" {
			msg = fmt.Sprintf("Working on it... Follow my progress here: <%s/jobs/%s>", bobURL, activeJobID)
		}
		_, _, _ = client.PostMessage(ev.Channel,
			slack.MsgOptionText(msg, false),
			slack.MsgOptionTS(threadTS),
		)

		result, err = orch.HandleReply(ctx, activeJobID, userText)
	} else {
		// New request — parse intent and start planning.
		// Need full thread context for intent parsing.
		var messages []Message
		if ev.ThreadTimeStamp != "" {
			replies, _, _, err := client.GetConversationReplies(&slack.GetConversationRepliesParameters{
				ChannelID: ev.Channel,
				Timestamp: ev.ThreadTimeStamp,
			})
			if err != nil {
				log.Printf("failed to get thread replies: %v", err)
				messages = []Message{{Role: RoleUser, Content: userText}}
			} else {
				messages = threadToMessages(replies, botUserID)
			}
		} else {
			messages = []Message{{Role: RoleUser, Content: userText}}
		}

		result, err = orch.HandleNewRequest(ctx, messages, func(jobID string) {
			msg := "Working on a plan..."
			if bobURL != "" {
				msg = fmt.Sprintf("Working on a plan... Follow my progress here: <%s/jobs/%s>", bobURL, jobID)
			}
			_, _, _ = client.PostMessage(ev.Channel,
				slack.MsgOptionText(msg, false),
				slack.MsgOptionTS(threadTS),
			)
		})
	}

	removeReaction(client, ev.Channel, ev.TimeStamp)

	if err != nil {
		log.Printf("orchestrator error: %v", err)
		text := fmt.Sprintf("<@%s> Sorry, I hit an error trying to respond. Please try again.", ev.User)
		_, _, err = client.PostMessage(ev.Channel,
			slack.MsgOptionText(text, false),
			slack.MsgOptionTS(threadTS),
		)
		if err != nil {
			log.Printf("failed to post message: %v", err)
		}
		return
	}

	// Plan with Block Kit blocks.
	if len(result.PlanBlocks) > 0 {
		// If there's a previous plan message for this job, remove its button.
		if state, ok := hub.GetJobState(result.JobID); ok && state.PlanMsgTS != "" {
			updatedBlocks := formatApprovedPlanBlocks(state.PlanContent, "superseded by updated plan")
			_, _, _, updateErr := client.UpdateMessage(ev.Channel, state.PlanMsgTS,
				slack.MsgOptionText(result.PlanText, false),
				slack.MsgOptionBlocks(updatedBlocks...),
			)
			if updateErr != nil {
				log.Printf("failed to update old plan message: %v", updateErr)
			}
		}

		planText := fmt.Sprintf("<@%s> %s", ev.User, result.PlanText)
		_, msgTS, postErr := client.PostMessage(ev.Channel,
			slack.MsgOptionText(planText, false),
			slack.MsgOptionBlocks(result.PlanBlocks...),
			slack.MsgOptionTS(threadTS),
		)
		if postErr != nil {
			log.Printf("failed to post plan message: %v", postErr)
		} else if state, ok := hub.GetJobState(result.JobID); ok {
			state.PlanMsgTS = msgTS
		}
		return
	}

	// Standard text reply.
	var text string
	if result.IsJob && result.PRURL != "" {
		text = fmt.Sprintf("<@%s> Done! %s", ev.User, result.PRURL)
	} else if result.IsJob && result.Text != "" {
		text = fmt.Sprintf("<@%s> %s", ev.User, result.Text)
	} else if result.IsJob {
		text = fmt.Sprintf("<@%s> Done!", ev.User)
	} else {
		text = fmt.Sprintf("<@%s> %s", ev.User, result.Text)
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

// NewSlackInteractionHandler handles Slack interactive component callbacks (button clicks).
func NewSlackInteractionHandler(client *slack.Client, signingSecret string, approver *Approver) http.Handler {
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

		// Parse the interaction payload.
		r.Body = io.NopCloser(strings.NewReader(string(body)))
		if err := r.ParseForm(); err != nil {
			http.Error(w, "failed to parse form", http.StatusBadRequest)
			return
		}

		payloadStr := r.FormValue("payload")
		if payloadStr == "" {
			http.Error(w, "missing payload", http.StatusBadRequest)
			return
		}

		var callback slack.InteractionCallback
		if err := json.Unmarshal([]byte(payloadStr), &callback); err != nil {
			http.Error(w, "failed to parse payload", http.StatusBadRequest)
			return
		}

		if callback.Type != slack.InteractionTypeBlockActions {
			w.WriteHeader(http.StatusOK)
			return
		}

		for _, action := range callback.ActionCallback.BlockActions {
			if action.ActionID != "approve_plan" {
				continue
			}

			jobID := action.Value
			channel := callback.Channel.ID
			threadTS := callback.Message.ThreadTimestamp
			if threadTS == "" {
				threadTS = callback.Message.Timestamp
			}
			approvedBy := fmt.Sprintf("<@%s>", callback.User.ID)

			// Return 200 immediately — Slack requires <3s response.
			w.WriteHeader(http.StatusOK)

			go approver.Approve(context.Background(), jobID, channel, threadTS, approvedBy)
			return
		}

		w.WriteHeader(http.StatusOK)
	})
}
