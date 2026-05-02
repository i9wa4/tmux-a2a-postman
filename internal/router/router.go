package router

import "github.com/i9wa4/tmux-a2a-postman/internal/nodeaddr"

type FailureReason string

const (
	FailureNone           FailureReason = ""
	FailureUnknownNode    FailureReason = "unknown_node"
	FailureUnknownSession FailureReason = "unknown_session"
)

type Resolution struct {
	Address         string
	SessionName     string
	NodeName        string
	ExplicitSession bool
	Found           bool
	FailureReason   FailureReason
}

type ExistsFunc func(string) bool

// Resolve maps a node address into the current runtime node key space.
//
// Bare addresses are scoped to sourceSessionName. Cross-session routing is
// explicit only: callers must pass session:node to leave the source session.
func Resolve(address, sourceSessionName string, nodeExists ExistsFunc, sessionExists ExistsFunc) Resolution {
	sessionName, nodeName, hasSession := nodeaddr.Split(address)
	if hasSession {
		result := Resolution{
			Address:         address,
			SessionName:     sessionName,
			NodeName:        nodeName,
			ExplicitSession: true,
		}
		if nodeExists != nil && nodeExists(address) {
			result.Found = true
			return result
		}
		if sessionExists != nil && !sessionExists(sessionName) {
			result.FailureReason = FailureUnknownSession
			return result
		}
		result.FailureReason = FailureUnknownNode
		return result
	}

	fullAddress := nodeaddr.Full(address, sourceSessionName)
	result := Resolution{
		Address:     fullAddress,
		SessionName: sourceSessionName,
		NodeName:    nodeName,
	}
	if nodeExists != nil && nodeExists(fullAddress) {
		result.Found = true
		return result
	}
	result.FailureReason = FailureUnknownNode
	return result
}
