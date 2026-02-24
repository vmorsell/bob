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
}

// NewApprover creates an Approver.
func NewApprover(slackClient *slack.Client, hub *Hub, orch *Orchestrator) *Approver {
	return &Approver{
		slackClient:  slackClient,
		hub:          hub,
		orchestrator: orch,
	}
}

// Approve runs the implementation for an approved plan. It is safe to call from
// multiple goroutines; TryStartImplementation provides an atomic guard.
func (a *Approver) Approve(ctx context.Context, jobID, channel, threadTS, approvedBy string) {
	if !a.hub.TryStartImplementation(jobID) {
		log.Printf("approve: job %s already implementing or wrong phase, ignoring", jobID)
		return
	}

	a.hub.Emit(jobID, EventPlanApproved, map[string]any{
		"approved_by": approvedBy,
	})

	// Ensure context has Slack thread info.
	ctx = WithSlackThread(ctx, channel, threadTS)
	ctx = WithHub(ctx, a.hub)

	// Update the plan message: remove button, show "Approved by ...".
	state, ok := a.hub.GetJobState(jobID)
	if ok && state.PlanMsgTS != "" {
		blocks := formatApprovedPlanBlocks(state.PlanContent, approvedBy)
		_, _, _, err := a.slackClient.UpdateMessage(channel, state.PlanMsgTS,
			slack.MsgOptionText(formatPlanMessage(state.PlanContent), false),
			slack.MsgOptionBlocks(blocks...),
		)
		if err != nil {
			log.Printf("approve: failed to update plan message: %v", err)
		}
	}

	// Post "Implementing..." message to thread.
	_, _, err := a.slackClient.PostMessage(channel,
		slack.MsgOptionText("Implementing approved plan...", false),
		slack.MsgOptionTS(threadTS),
	)
	if err != nil {
		log.Printf("approve: failed to post implementing message: %v", err)
	}

	result, err := a.orchestrator.HandleApproval(ctx, jobID)

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
