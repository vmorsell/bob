package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"

	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
)

func NewSlackHandler(botToken, signingSecret string) http.Handler {
	client := slack.New(botToken)

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
				_, _, err := client.PostMessage(ev.Channel,
					slack.MsgOptionText("Hey! I'm Bob. I heard you.", false),
				)
				if err != nil {
					log.Printf("failed to post message: %v", err)
				}
			}
		}
	})
}
