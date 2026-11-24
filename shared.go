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
	// Deprecated: Use ModelS21Pro instead.
	ModelSpeech15 Model = "speech-1.5"
	// Deprecated: Use ModelS21Pro instead.
	ModelSpeech16 Model = "speech-1.6"
	// ModelS1 is the S1 model.
	//
	// Deprecated: s1 retires on 2026-12-31. Use ModelS21Pro instead.
	ModelS1 Model = "s1"
	// ModelS2Pro is the previous-generation S2 Pro model.
	ModelS2Pro Model = "s2-pro"
	// ModelS21Pro is the current flagship model and the SDK default.
	ModelS21Pro Model = "s2.1-pro"
	// ModelS21ProFree is the free tier of S2.1 Pro, available until 2026-11-30.
	ModelS21ProFree Model = "s2.1-pro-free"
	// ModelDrama3 is the Drama 3 model.
	ModelDrama3 Model = "drama-3"
)
