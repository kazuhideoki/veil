package infra

import "testing"

func TestItemIDFromJSONAcceptsOnePasswordIDFields(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{name: "id", body: `{"id":"abc123"}`, want: "abc123"},
		{name: "uuid", body: `{"uuid":"def456"}`, want: "def456"},
		{name: "item_id", body: `{"item_id":"ghi789"}`, want: "ghi789"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := itemIDFromJSON([]byte(tt.body))
			if err != nil {
				t.Fatalf("itemIDFromJSON() returned error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("id = %q, want %q", got, tt.want)
			}
		})
	}
}
