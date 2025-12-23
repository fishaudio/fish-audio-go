package fishaudio

import "testing"

func TestAudioFormat_Values(t *testing.T) {
	tests := []struct {
		format   AudioFormat
		expected string
	}{
		{AudioFormatMP3, "mp3"},
		{AudioFormatWAV, "wav"},
		{AudioFormatPCM, "pcm"},
		{AudioFormatOpus, "opus"},
	}

	for _, tt := range tests {
		t.Run(string(tt.format), func(t *testing.T) {
			if string(tt.format) != tt.expected {
				t.Errorf("AudioFormat = %q, want %q", string(tt.format), tt.expected)
			}
		})
	}
}

func TestLatencyMode_Values(t *testing.T) {
	tests := []struct {
		mode     LatencyMode
		expected string
	}{
		{LatencyNormal, "normal"},
		{LatencyBalanced, "balanced"},
	}

	for _, tt := range tests {
		t.Run(string(tt.mode), func(t *testing.T) {
			if string(tt.mode) != tt.expected {
				t.Errorf("LatencyMode = %q, want %q", string(tt.mode), tt.expected)
			}
		})
	}
}

func TestVisibility_Values(t *testing.T) {
	tests := []struct {
		visibility Visibility
		expected   string
	}{
		{VisibilityPublic, "public"},
		{VisibilityUnlist, "unlist"},
		{VisibilityPrivate, "private"},
	}

	for _, tt := range tests {
		t.Run(string(tt.visibility), func(t *testing.T) {
			if string(tt.visibility) != tt.expected {
				t.Errorf("Visibility = %q, want %q", string(tt.visibility), tt.expected)
			}
		})
	}
}

func TestTrainMode_Values(t *testing.T) {
	if string(TrainModeFast) != "fast" {
		t.Errorf("TrainModeFast = %q, want %q", string(TrainModeFast), "fast")
	}
}

func TestModelState_Values(t *testing.T) {
	tests := []struct {
		state    ModelState
		expected string
	}{
		{ModelStateCreated, "created"},
		{ModelStateTraining, "training"},
		{ModelStateTrained, "trained"},
		{ModelStateFailed, "failed"},
	}

	for _, tt := range tests {
		t.Run(string(tt.state), func(t *testing.T) {
			if string(tt.state) != tt.expected {
				t.Errorf("ModelState = %q, want %q", string(tt.state), tt.expected)
			}
		})
	}
}

func TestModel_Values(t *testing.T) {
	tests := []struct {
		model    Model
		expected string
	}{
		{ModelSpeech15, "speech-1.5"},
		{ModelSpeech16, "speech-1.6"},
		{ModelS1, "s1"},
	}

	for _, tt := range tests {
		t.Run(string(tt.model), func(t *testing.T) {
			if string(tt.model) != tt.expected {
				t.Errorf("Model = %q, want %q", string(tt.model), tt.expected)
			}
		})
	}
}

func TestPaginatedResponse(t *testing.T) {
	// Test that PaginatedResponse can be instantiated with different types
	type Item struct {
		ID   string
		Name string
	}

	resp := PaginatedResponse[Item]{
		Total: 100,
		Items: []Item{
			{ID: "1", Name: "First"},
			{ID: "2", Name: "Second"},
		},
	}

	if resp.Total != 100 {
		t.Errorf("Total = %d, want %d", resp.Total, 100)
	}
	if len(resp.Items) != 2 {
		t.Errorf("Items length = %d, want %d", len(resp.Items), 2)
	}
	if resp.Items[0].ID != "1" {
		t.Errorf("Items[0].ID = %q, want %q", resp.Items[0].ID, "1")
	}
}
