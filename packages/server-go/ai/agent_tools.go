package ai

import (
	"fmt"
	"strings"
)

// executeAgentRequestMember handles agent_request_member tool calls.
// Sends a request to the lead to spawn a new team member with the specified role.
func executeAgentRequestMember(input map[string]any, ctx *ChatContext) (string, bool) {
	role := strings.TrimSpace(fmt.Sprintf("%v", input["role"]))
	brief := strings.TrimSpace(fmt.Sprintf("%v", input["brief"]))

	if role == "" {
		return "role required", true
	}
	if brief == "" {
		return "brief required", true
	}

	if ctx.AgentComms == nil {
		return "no comms hub available", true
	}

	// Send request to lead with structured format
	subject := fmt.Sprintf("Request new member: %s", role)
	msgBody := fmt.Sprintf("role:%s\nbrief:%s", role, brief)

	msg, err := ctx.AgentComms.Send(ctx.AgentName, "lead", subject, msgBody, "")
	if err != nil {
		return fmt.Sprintf("send failed: %v", err), true
	}

	return fmt.Sprintf("member request sent (msg %s)", msg.ID), false
}

// executeAgentRetire handles agent_retire tool calls.
// Signals to the lead that this member is done and should be released.
func executeAgentRetire(input map[string]any, ctx *ChatContext) (string, bool) {
	reason := strings.TrimSpace(fmt.Sprintf("%v", input["reason"]))
	if reason == "" {
		reason = "work complete"
	}

	if ctx.AgentComms == nil {
		return "no comms hub available", true
	}

	// Send retire notification to lead
	subject := "Agent retiring"
	msgBody := fmt.Sprintf("reason:%s", reason)

	msg, err := ctx.AgentComms.Send(ctx.AgentName, "lead", subject, msgBody, "")
	if err != nil {
		return fmt.Sprintf("send failed: %v", err), true
	}

	return fmt.Sprintf("retirement signaled (msg %s)", msg.ID), false
}
