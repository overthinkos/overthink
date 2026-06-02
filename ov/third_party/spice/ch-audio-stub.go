//go:build !spice_audio

package spice

// Audio (playback/record) channels are DISABLED in this build. They are the
// ONLY consumers of the opus + portaudio cgo bindings; gating them behind the
// `spice_audio` build tag lets this library — and ov, which embeds it for the
// framebuffer / input / cursor / clipboard channels only and never touches
// audio — link and RUN without libopus / libportaudio / libasound / libjack.
// Build with `-tags spice_audio` (and install opusfile + portaudio) to enable
// SPICE audio playback/recording.
//
// These stubs let client.go compile unchanged: the Client struct keeps its
// `playback *ChPlayback` / `record *ChRecord` fields and its mute accessors,
// and the channel dispatch still calls setupPlayback / setupRecord — which here
// return nil, so the audio channels are simply never connected. Every other
// channel (main, display, inputs, cursor, clipboard, webdav) is unaffected.

// ChPlayback is a stub for the audio playback channel (disabled in this build).
// The mute field exists only so client.go's mute accessors compile.
type ChPlayback struct {
	mute bool
}

// ChRecord is a stub for the audio record channel (disabled in this build).
type ChRecord struct{}

// setupPlayback / setupRecord are no-ops in the audio-disabled build: they
// return nil so the dispatch leaves cl.playback / cl.record unset and the audio
// channels are never connected. No opus/portaudio symbols are referenced.
func (cl *Client) setupPlayback(id uint8) (*ChPlayback, error) { return nil, nil }
func (cl *Client) setupRecord(id uint8) (*ChRecord, error)     { return nil, nil }
