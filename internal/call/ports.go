package call

import "context"

// Transcriber turns microphone audio into finalized text utterances.
// For Plan 1, the fake reads lines from stdin.
type Transcriber interface {
	// Utterances returns a channel that yields a value every time a complete
	// user utterance is available. The channel is closed when Stop is called.
	Utterances() <-chan string
	Stop()
}

// Speaker turns text into spoken audio. For Plan 1, the fake prints to stdout.
// Speak blocks until playback completes or ctx is canceled.
type Speaker interface {
	Speak(ctx context.Context, text string) error
}

// PM is the conversational brain (Haiku in production, scripted in Plan 1).
type PM interface {
	Intake(ctx context.Context, in IntakeInput) (PMIntakeResult, error)
	Route(ctx context.Context, in RouteInput) (PMRouteResult, error)
	// Reset clears conversation history. Called at the start of every new call.
	Reset()
}

// Hotkey emits an event whenever the call hotkey is pressed.
// For Plan 1, the fake reads blank lines from stdin.
type Hotkey interface {
	Events() <-chan struct{}
	Stop()
}
