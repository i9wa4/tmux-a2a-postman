package verdictgate

import "testing"

// TestIsExemptSender_TruthTable (#764 X2): the verdict-debt exemption belongs
// only to the configured interface node. There is no built-in node name, so an
// unset or empty interface_node exempts nobody, including a node that happens
// to be called "messenger".
func TestIsExemptSender_TruthTable(t *testing.T) {
	tests := []struct {
		name          string
		sender        string
		interfaceNode string
		want          bool
	}{
		{name: "unset interface node, messenger sender", sender: "messenger", interfaceNode: "", want: false},
		{name: "unset interface node, worker sender", sender: "worker", interfaceNode: "", want: false},
		{name: "interface node messenger, messenger sender", sender: "messenger", interfaceNode: "messenger", want: true},
		{name: "interface node messenger, worker sender", sender: "worker", interfaceNode: "messenger", want: false},
		{name: "interface node mouthpiece, mouthpiece sender", sender: "mouthpiece", interfaceNode: "mouthpiece", want: true},
		{name: "interface node mouthpiece, messenger sender is not exempt", sender: "messenger", interfaceNode: "mouthpiece", want: false},
		{name: "empty sender never matches an unset interface node", sender: "", interfaceNode: "", want: false},
		{name: "case differs: no fuzzy match", sender: "Messenger", interfaceNode: "messenger", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsExemptSender(tt.sender, tt.interfaceNode); got != tt.want {
				t.Fatalf("IsExemptSender(%q, %q) = %v, want %v", tt.sender, tt.interfaceNode, got, tt.want)
			}
		})
	}
}

// TestIsExemptInSession_TruthTable (#624/#700 F-700-R2-2): the exemption is
// bound to the daemon's own session. A same-bare-named node in a peer session,
// or any node when the own session is unknown, is not exempt.
func TestIsExemptInSession_TruthTable(t *testing.T) {
	tests := []struct {
		name          string
		sender        string
		interfaceNode string
		senderSession string
		ownSession    string
		want          bool
	}{
		{name: "own session interface node", sender: "human", interfaceNode: "human", senderSession: "own", ownSession: "own", want: true},
		{name: "peer session same bare name", sender: "human", interfaceNode: "human", senderSession: "peer", ownSession: "own", want: false},
		{name: "own session other node", sender: "worker", interfaceNode: "human", senderSession: "own", ownSession: "own", want: false},
		{name: "unknown own session fails closed", sender: "human", interfaceNode: "human", senderSession: "own", ownSession: "", want: false},
		{name: "unknown own session and empty sender session", sender: "human", interfaceNode: "human", senderSession: "", ownSession: "", want: false},
		{name: "no interface node configured", sender: "human", interfaceNode: "", senderSession: "own", ownSession: "own", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsExemptInSession(tt.sender, tt.interfaceNode, tt.senderSession, tt.ownSession); got != tt.want {
				t.Fatalf("IsExemptInSession(%q, %q, %q, %q) = %v, want %v", tt.sender, tt.interfaceNode, tt.senderSession, tt.ownSession, got, tt.want)
			}
		})
	}
}
