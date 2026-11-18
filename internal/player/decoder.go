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
	dec    *mp3.Decoder
	format Format
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
	return &mp3Decoder{dec: dec, format: Format{SampleRate: rate, Channels: 2}}, nil
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
	stream *flac.Stream
	format Format
	buf    []byte
}

func newFLACDecoder(r io.ReadSeeker) (decoder, error) {
	if _, err := r.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	stream, err := flac.New(bufio.NewReaderSize(r, 128<<10))
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
	return &flacDecoder{stream: stream, format: Format{SampleRate: rate, Channels: channels}}, nil
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
