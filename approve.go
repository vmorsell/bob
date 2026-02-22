package main

import (
	"context"
	"fmt"
	"log"

	"github.com/slack-go/slack"
)

// Approver provides a shared approval path used by both Slack button interactions
// and the web UI approve endpoint.
type Approver struct {
	slackClient  *slack.Client
	hub          *Hub
	orchestrator *Orchestrator
	botUserID    string
}

// NewApprover creates an Approver.
func NewApprover(slackClient *slack.Client, hub *Hub, orch *Orchestrator, botUserID string) *Approver {
	return &Approver{
		slackClient:  slackClient,
		hub:          hub,
		orchestrator: orch,
		botUserID:    botUserID,
	}
}

// Approve runs the implementation for an approved plan. It is safe to call from
// multiple goroutines; TryStartImplementation provides an atomic guard.
func (a *Approver) Approve(ctx context.Context, jobID, channel, threadTS, approvedBy string) {
	if !a.hub.TryStartImplementation(jobID) {
		log.Printf("approve: job %s already implementing, ignoring duplicate", jobID)
		return
	}

	// Update the plan message: remove button, show "Approved by ...".
	if planTS := a.hub.GetPlanMsgTS(jobID); planTS != "" {
		// Fetch the existing message to get the plan text.
		msgs, _, _, err := a.slackClient.GetConversationReplies(&slack.GetConversationRepliesParameters{
			ChannelID: channel,
			Timestamp: planTS,
			Inclusive: true,
			Limit:     1,
		})
		if err == nil && len(msgs) > 0 {
			plan := extractPlanFromThread([]Message{{Role: RoleAssistant, Content: msgs[0].Text}})
			blocks := formatApprovedPlanBlocks(plan, approvedBy)
			_, _, _, err = a.slackClient.UpdateMessage(channel, planTS,
				slack.MsgOptionText(msgs[0].Text, false),
				slack.MsgOptionBlocks(blocks...),
			)
			if err != nil {
				log.Printf("approve: failed to update plan message: %v", err)
			}
		}
	}

	// Post "implementing" message to thread.
	_, _, err := a.slackClient.PostMessage(channel,
		slack.MsgOptionText("Implementing approved plan...", false),
		slack.MsgOptionTS(threadTS),
	)
	if err != nil {
		log.Printf("approve: failed to post implementing message: %v", err)
	}

	// Fetch thread messages for orchestrator context.
	replies, _, _, err := a.slackClient.GetConversationReplies(&slack.GetConversationRepliesParameters{
		ChannelID: channel,
		Timestamp: threadTS,
	})
	if err != nil {
		log.Printf("approve: failed to get thread replies: %v", err)
		a.hub.ClearImplementation(jobID)
		return
	}

	messages := threadToMessages(replies, a.botUserID)

	// Build context with Slack thread info.
	ctx = WithSlackThread(ctx, channel, threadTS)
	ctx = WithHub(ctx, a.hub)

	result, err := a.orchestrator.ImplementApprovedPlan(ctx, messages)

	var text string
	if err != nil {
		log.Printf("approve: orchestrator error: %v", err)
		text = fmt.Sprintf("Sorry, I hit an error trying to implement: %s", err.Error())
		a.hub.ClearImplementation(jobID)
	} else if result.PRURL != "" {
		text = fmt.Sprintf("Done! %s", result.PRURL)
	} else if result.Text != "" {
		text = result.Text
	} else {
		text = "Done!"
	}

	_, _, err = a.slackClient.PostMessage(channel,
		slack.MsgOptionText(text, false),
		slack.MsgOptionTS(threadTS),
	)
	if err != nil {
		log.Printf("approve: failed to post result: %v", err)
	}
}
