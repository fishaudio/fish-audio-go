package fishaudio

// AudioFormat specifies the output audio format.
type AudioFormat string

const (
	AudioFormatMP3  AudioFormat = "mp3"
	AudioFormatWAV  AudioFormat = "wav"
	AudioFormatPCM  AudioFormat = "pcm"
	AudioFormatOpus AudioFormat = "opus"
)

// LatencyMode specifies the generation latency mode.
type LatencyMode string

const (
	LatencyNormal   LatencyMode = "normal"
	LatencyBalanced LatencyMode = "balanced"
)

// PaginatedResponse wraps paginated API responses.
type PaginatedResponse[T any] struct {
	Total int `json:"total"`
	Items []T `json:"items"`
}

// Visibility specifies the visibility of a voice model.
type Visibility string

const (
	VisibilityPublic  Visibility = "public"
	VisibilityUnlist  Visibility = "unlist"
	VisibilityPrivate Visibility = "private"
)

// TrainMode specifies the training mode for voice models.
type TrainMode string

const (
	TrainModeFast TrainMode = "fast"
)

// ModelState specifies the state of a voice model.
type ModelState string

const (
	ModelStateCreated  ModelState = "created"
	ModelStateTraining ModelState = "training"
	ModelStateTrained  ModelState = "trained"
	ModelStateFailed   ModelState = "failed"
)

// Model specifies the TTS model to use.
type Model string

const (
	ModelSpeech15 Model = "speech-1.5"
	ModelSpeech16 Model = "speech-1.6"
	ModelS1       Model = "s1"
)
