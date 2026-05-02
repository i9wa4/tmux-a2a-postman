package binding

import "testing"

func TestValidateNodeName(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want bool
	}{
		{name: "lowercase", in: "worker", want: true},
		{name: "uppercase", in: "Worker-1", want: true},
		{name: "digit start", in: "1-worker", want: true},
		{name: "empty", in: "", want: false},
		{name: "space", in: "bad node", want: false},
		{name: "underscore", in: "bad_node", want: false},
		{name: "too long", in: "a1234567890123456789012345678901234567890123456789012345678901234", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ValidateNodeName(tt.in); got != tt.want {
				t.Fatalf("ValidateNodeName(%q) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}
