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
