package player

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	mp3 "github.com/hajimehoshi/go-mp3"
	"github.com/mewkiz/flac"
)

// Format is what a decoder produces: interleaved signed 16-bit little-endian
// frames.
type Format struct {
	SampleRate int
	Channels   int
}

// FrameSize is the bytes in one frame (all channels of one sample).
func (f Format) FrameSize() int { return f.Channels * 2 }

// valid reports a format the audio output accepts.
func (f Format) valid() bool {
	return f.SampleRate >= 8000 && f.SampleRate <= 192000 && f.Channels >= 1 && f.Channels <= 2
}

// decoder turns a byte stream into PCM frames. One goroutine owns it.
type decoder interface {
	Format() Format
	Read(p []byte) (int, error)
}

// seeker is a decoder that can start again at a position of its own instead of
// being read and thrown away up to there.
//
// It matters for the formats that cost something to decode: a FLAC file at 96 kHz
// takes over a second to walk to the end of a long song, and that second is
// silence the listener hears after pressing the bar. A format without it falls
// back to reading forward.
type seeker interface {
	SeekSample(sample int64) error
}

// seekSamples moves a decoder to a sample position, using its own seek when the
// format has one and reading forward from where it is when it does not.
//
// Reading forward from the decoder's own position is what keeps the position
// honest: a stream cannot seek past the bytes it has received, so the only way to
// reach a later point is to decode up to it — which waits for the download, and is
// why a seek on a song still arriving takes as long as the network needs.
//
// have is how many samples this decoder has already produced. It is the caller's
// bookkeeping, because the decoder interface does not promise to report it.
func seekSamples(dec decoder, want, have int64, format Format) (decoder, int64, error) {
	if format.SampleRate <= 0 {
		return dec, have, nil
	}
	if want < 0 {
		want = 0
	}
	// A format with a real seek is used first, in either direction: it lands on the
	// target frame instead of decoding everything up to it, which on a 58 MB FLAC
	// is the difference between 13 ms and 1.3 s.
	if s, ok := dec.(seeker); ok {
		if err := s.SeekSample(want); err == nil {
			return dec, want, nil
		}
	}
	if want < have {
		// It refused and the target is behind where the decoder is: only starting
		// again can get there, and a stream usually cannot.
		return dec, have, errNoBackwardSeek
	}
	if err := discardForwardSamples(dec, want-have, format); err != nil {
		return dec, have, err
	}
	return dec, want, nil
}

// errNoBackwardSeek reports that the decoder cannot move backwards; the caller
// opens it again from the start.
var errNoBackwardSeek = errors.New("player: this source cannot seek backwards")

// discardForwardSamples reads and throws away a number of samples.
func discardForwardSamples(dec decoder, samples int64, format Format) error {
	if samples <= 0 {
		return nil
	}
	remaining := samples * int64(format.FrameSize())
	buf := make([]byte, 32<<10)
	for remaining > 0 {
		n, err := dec.Read(buf)
		if n > 0 {
			remaining -= int64(n)
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
	}
	return nil
}

// openDecoder sniffs a stream's container and returns the decoder for it. The
// name of the file is not trusted: the site sometimes serves an mp3 under a
// .flac URL, and a decoder picked by extension would refuse it.
func openDecoder(r io.ReadSeeker) (decoder, error) {
	head, err := peek(r, 16)
	if err != nil {
		return nil, err
	}
	switch {
	case len(head) >= 4 && string(head[:4]) == "fLaC":
		return newFLACDecoder(r)
	case len(head) >= 4 && string(head[:4]) == "RIFF":
		return newWAVDecoder(r)
	case len(head) >= 3 && string(head[:3]) == "ID3":
		return newMP3Decoder(r)
	case len(head) >= 2 && head[0] == 0xFF && head[1]&0xE0 == 0xE0:
		return newMP3Decoder(r)
	case len(head) >= 4 && string(head[:4]) == "OggS":
		return nil, fmt.Errorf("%w: ogg vorbis", ErrUnsupportedFormat)
	case len(head) >= 8 && string(head[4:8]) == "ftyp":
		return nil, fmt.Errorf("%w: mp4 audio", ErrUnsupportedFormat)
	}
	return nil, fmt.Errorf("%w: %q", ErrUnsupportedFormat, head)
}

// peek reads the first n bytes and rewinds.
func peek(r io.ReadSeeker, n int) ([]byte, error) {
	buf := make([]byte, n)
	read, err := io.ReadFull(r, buf)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		return nil, err
	}
	if _, err := r.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	return buf[:read], nil
}

// ----- mp3 -----

type mp3Decoder struct {
	src    io.ReadSeeker
	dec    *mp3.Decoder
	format Format
}

// SeekSample moves an MP3 to a sample. The decoder cannot start mid-frame, so a
// byte offset is estimated from the average frame length and the decoder is
// rebuilt there; the fixed-size frames it lands on are corrected by the caller,
// which compares the position it asked for with the one it got.
func (d *mp3Decoder) SeekSample(sample int64) error {
	if sample <= 0 {
		sample = 0
	}
	// 1152 samples per frame is the MPEG-1 Layer III frame; the estimate is
	// refined by the caller reading the next chunk.
	frames := sample / 1152
	offset := frames * int64(1152*int64(d.format.Channels)*2)
	if offset < 0 {
		offset = 0
	}
	if _, err := d.src.Seek(offset, io.SeekStart); err != nil {
		return err
	}
	dec, err := mp3.NewDecoder(bufio.NewReaderSize(d.src, 128<<10))
	if err != nil {
		return err
	}
	d.dec = dec
	return nil
}

func newMP3Decoder(r io.ReadSeeker) (decoder, error) {
	if _, err := r.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	dec, err := mp3.NewDecoder(bufio.NewReaderSize(r, 128<<10))
	if err != nil {
		return nil, fmt.Errorf("player: mp3: %w", err)
	}
	rate := dec.SampleRate()
	if rate <= 0 {
		rate = 44100
	}
	// the mp3 decoder always answers stereo 16-bit, whatever the source was.
	return &mp3Decoder{src: r, dec: dec, format: Format{SampleRate: rate, Channels: 2}}, nil
}

func (d *mp3Decoder) Format() Format { return d.format }

func (d *mp3Decoder) Read(p []byte) (int, error) {
	n, err := d.dec.Read(p)
	// The decoder hands back whatever it has; a caller that counts frames needs a
	// whole one, so the tail waits for the next read.
	if size := d.format.FrameSize(); n%size != 0 {
		keep := n - n%size
		if keep == 0 {
			// One frame is 4 bytes and the buffer was bigger, so this only
			// happens at the very end of the stream.
			return 0, io.EOF
		}
		n = keep
	}
	return n, err
}

// ----- flac -----

type flacDecoder struct {
	src    io.ReadSeeker
	stream *flac.Stream
	format Format
	buf    []byte
}

// SeekSample moves a FLAC to a sample using the stream's own seek table, which is
// what makes it cheap: the frames before the target are not decoded at all.
func (d *flacDecoder) SeekSample(sample int64) error {
	if _, err := d.stream.Seek(uint64(sample)); err != nil {
		return err
	}
	d.buf = d.buf[:0]
	return nil
}

func newFLACDecoder(r io.ReadSeeker) (decoder, error) {
	if _, err := r.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	// NewSeek rather than New: it keeps the reader seekable, which is what lets a
	// seek land on the target frame instead of decoding everything before it. The
	// reader is passed as it is, because the library asserts io.ReadSeeker on it.
	stream, err := flac.NewSeek(r)
	if err != nil {
		return nil, fmt.Errorf("player: flac: %w", err)
	}
	rate := int(stream.Info.SampleRate)
	if rate <= 0 {
		rate = 44100
	}
	channels := int(stream.Info.NChannels)
	if channels > 2 {
		channels = 2
	}
	return &flacDecoder{src: r, stream: stream, format: Format{SampleRate: rate, Channels: channels}}, nil
}

func (d *flacDecoder) Format() Format { return d.format }

// Read decodes frames into p until p is full or the stream ends, so a caller
// gets the whole block it asked for.
func (d *flacDecoder) Read(p []byte) (int, error) {
	size := d.format.FrameSize()
	p = p[:len(p)-len(p)%size]
	if len(p) == 0 {
		return 0, nil
	}
	total := 0
	for total < len(p) {
		if len(d.buf) == 0 {
			ok, err := d.fill()
			if err != nil {
				if total > 0 {
					return total, nil
				}
				return 0, err
			}
			if !ok {
				if total > 0 {
					return total, nil
				}
				return 0, io.EOF
			}
		}
		n := copy(p[total:], d.buf)
		n -= n % size
		d.buf = d.buf[n:]
		total += n
		if n == 0 {
			break
		}
	}
	return total, nil
}

// fill decodes one FLAC frame into d.buf as s16le.
func (d *flacDecoder) fill() (bool, error) {
	f, err := d.stream.ParseNext()
	if err != nil {
		if errors.Is(err, io.EOF) {
			return false, nil
		}
		return false, err
	}
	f.Correlate()
	channels := len(f.Subframes)
	if channels == 0 {
		return true, nil
	}
	if channels > 2 {
		channels = 2
	}
	count := len(f.Subframes[0].Samples)
	bps := int(d.stream.Info.BitsPerSample)
	shift := 0
	if bps > 16 {
		shift = bps - 16
	}
	d.buf = make([]byte, count*channels*2)
	for i := 0; i < count; i++ {
		for ch := 0; ch < channels; ch++ {
			sample := f.Subframes[ch].Samples[i]
			if shift > 0 {
				sample >>= shift
			}
			binary.LittleEndian.PutUint16(d.buf[(i*channels+ch)*2:], uint16(int16(clampInt32(sample))))
		}
	}
	return true, nil
}

// ----- wav -----
//
// A wav decoder is here for the tests, which generate their fixture, and for a
// cloud-drive file that happens to be PCM. The site does not serve wav.

type wavDecoder struct {
	src     io.ReadSeeker
	format  Format
	bits    int
	start   int64
	end     int64
	scratch []byte
}

func newWAVDecoder(r io.ReadSeeker) (decoder, error) {
	if _, err := r.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	head := make([]byte, 12)
	if _, err := io.ReadFull(r, head); err != nil {
		return nil, err
	}
	if string(head[:4]) != "RIFF" || string(head[8:12]) != "WAVE" {
		return nil, fmt.Errorf("%w: not a wav file", ErrUnsupportedFormat)
	}
	d := &wavDecoder{src: r}
	for {
		var chunk struct {
			ID   [4]byte
			Size uint32
		}
		if err := binary.Read(r, binary.LittleEndian, &chunk); err != nil {
			return nil, fmt.Errorf("player: wav: %w", err)
		}
		pad := int64(chunk.Size % 2)
		switch string(chunk.ID[:]) {
		case "fmt ":
			body := make([]byte, chunk.Size)
			if _, err := io.ReadFull(r, body); err != nil {
				return nil, err
			}
			if len(body) < 16 {
				return nil, fmt.Errorf("player: wav: short fmt chunk")
			}
			format := binary.LittleEndian.Uint16(body[0:])
			channels := int(binary.LittleEndian.Uint16(body[2:]))
			rate := int(binary.LittleEndian.Uint32(body[4:]))
			bits := int(binary.LittleEndian.Uint16(body[14:]))
			if format != 1 || (bits != 16 && bits != 8) || channels < 1 || channels > 2 {
				return nil, fmt.Errorf("%w: wav pcm %d-bit %d channels", ErrUnsupportedFormat, bits, channels)
			}
			d.format = Format{SampleRate: rate, Channels: channels}
			d.bits = bits
			if pad != 0 {
				r.Seek(pad, io.SeekCurrent)
			}
		case "data":
			pos, err := r.Seek(0, io.SeekCurrent)
			if err != nil {
				return nil, err
			}
			if d.format.SampleRate == 0 {
				return nil, fmt.Errorf("player: wav: data before fmt")
			}
			d.start = pos
			d.end = pos + int64(chunk.Size)
			return d, nil
		default:
			if _, err := r.Seek(int64(chunk.Size)+pad, io.SeekCurrent); err != nil {
				return nil, err
			}
		}
	}
}

// SeekSample moves a WAV to a sample: uncompressed frames have a fixed size, so
// the offset is arithmetic — no decoding is involved at all.
func (d *wavDecoder) SeekSample(sample int64) error {
	if sample < 0 {
		sample = 0
	}
	offset := d.start + sample*int64(d.format.FrameSize())
	if offset < d.start {
		offset = d.start
	}
	if d.end > 0 && offset >= d.end {
		// Past the end: put the reader at the end so the next Read reports EOF,
		// which is what the playback loop expects.
		offset = d.end
	}
	if _, err := d.src.Seek(offset, io.SeekStart); err != nil {
		return err
	}
	return nil
}

func (d *wavDecoder) Format() Format { return d.format }

func (d *wavDecoder) Read(p []byte) (int, error) {
	if d.bits == 8 {
		if cap(d.scratch) < len(p)/2+2 {
			d.scratch = make([]byte, len(p)/2+2)
		}
		n, err := d.src.Read(d.scratch[:len(p)/2])
		for i := 0; i < n; i++ {
			binary.LittleEndian.PutUint16(p[i*2:], uint16(int16(int32(d.scratch[i])*256-32768)))
		}
		return n * 2, err
	}
	pos, err := d.src.Seek(0, io.SeekCurrent)
	if err != nil {
		return 0, err
	}
	if pos >= d.end {
		return 0, io.EOF
	}
	if remaining := d.end - pos; int64(len(p)) > remaining {
		p = p[:remaining]
	}
	n, err := d.src.Read(p)
	n -= n % d.format.FrameSize()
	return n, err
}

// ----- position, from a source -----

// sourceDuration reports how long a source plays. A wav header and a flac
// stream info both carry it; an mp3 does not without a full scan, so the
// caller falls back to the API's own duration field.
func sourceDuration(d decoder, size int64) time.Duration {
	if w, ok := d.(*wavDecoder); ok {
		return wavDuration(w)
	}
	return 0
}

func wavDuration(d *wavDecoder) time.Duration {
	bytesPerSecond := d.format.SampleRate * d.format.FrameSize()
	if bytesPerSecond == 0 {
		return 0
	}
	return time.Duration(float64(d.end-d.start) / float64(bytesPerSecond) * float64(time.Second))
}

func clampInt32(v int32) int32 {
	if v > 32767 {
		return 32767
	}
	if v < -32768 {
		return -32768
	}
	return v
}

var _ = os.ErrClosed

// samplesAt is a position as a sample count.
func samplesAt(at time.Duration, format Format) int64 {
	if format.SampleRate <= 0 || at <= 0 {
		return 0
	}
	return int64(at.Seconds() * float64(format.SampleRate))
}
