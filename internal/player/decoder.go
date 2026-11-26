package player

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"time"

	mp3 "github.com/hajimehoshi/go-mp3"
	"github.com/mewkiz/flac"
	flacframe "github.com/mewkiz/flac/frame"
)

// Format is what a decoder produces: interleaved signed 16-bit little-endian
// PCM frames.
type Format struct {
	SampleRate int
	Channels   int
}

// FrameSize is the bytes in one frame (all channels of one sample).
func (f Format) FrameSize() int { return f.Channels * 2 }

// valid reports whether the audio output accepts this format.
func (f Format) valid() bool {
	return f.SampleRate >= 8000 && f.SampleRate <= 192000 && f.Channels >= 1 && f.Channels <= 2
}

// decoder turns a byte stream into signed 16-bit little-endian PCM frames.
// One goroutine owns it.
type decoder interface {
	Format() Format
	Duration() time.Duration
	Read(p []byte) (int, error)
}

// seeker is a decoder that can jump directly to a sample position instead of
// decoding and discarding everything before it.
type seeker interface {
	SeekSample(sample int64) error
}

// errNoBackwardSeek reports that the decoder cannot move backwards in place;
// the caller reopens the decoder from the beginning of the source.
var errNoBackwardSeek = errors.New("player: this source cannot seek backwards")

// errSeekUnavailable reports that fast direct seeking is not currently possible
// (for example, on an unfinished download), so seekSamples should fall back.
var errSeekUnavailable = errors.New("player: direct seek unavailable")

// seekSamples moves dec to sample position want. have is how many samples dec
// has already produced since the start of the stream.
func seekSamples(dec decoder, want, have int64, format Format) (decoder, int64, error) {
	if format.SampleRate <= 0 {
		return dec, have, nil
	}
	if want < 0 {
		want = 0
	}
	if s, ok := dec.(seeker); ok {
		if err := s.SeekSample(want); err == nil {
			return dec, want, nil
		}
	}
	if want < have {
		return dec, have, errNoBackwardSeek
	}
	if err := discardForwardSamples(dec, want-have, format); err != nil {
		return dec, have, err
	}
	return dec, want, nil
}

// discardForwardSamples reads and throws away an exact number of sample frames.
func discardForwardSamples(dec decoder, samples int64, format Format) error {
	if samples <= 0 {
		return nil
	}
	frameSize := format.FrameSize()
	if frameSize <= 0 {
		return nil
	}
	remaining := samples * int64(frameSize)
	buf := make([]byte, 32<<10)
	for remaining > 0 {
		toRead := len(buf)
		if int64(toRead) > remaining {
			toRead = int(remaining)
		}
		toRead -= toRead % frameSize
		if toRead == 0 {
			break
		}
		n, err := dec.Read(buf[:toRead])
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

// isCompleteSource reports whether r already holds every byte of the audio
// file (so a decoder can safely scan to EOF to build a seek index).
func isCompleteSource(r io.ReadSeeker) bool {
	if tf, ok := r.(*tailFile); ok {
		return tf.download.complete()
	}
	return true
}

// nonSeeker hides io.Seeker so mp3.NewDecoder starts streaming immediately
// without scanning an incomplete download to EOF.
type nonSeeker struct{ io.Reader }

// bufferedReadSeeker adds a 64 KiB read buffer while preserving io.ReadSeeker.
// go-mp3 reads small 4-byte headers when indexing frames; buffering turns
// thousands of tiny syscalls into a handful of fast block reads.
type bufferedReadSeeker struct {
	rs     io.ReadSeeker
	buf    []byte
	pos    int64 // logical stream offset
	bufPos int64 // stream offset of buf[0]
	bufLen int   // valid bytes in buf
}

func newBufferedReadSeeker(rs io.ReadSeeker) *bufferedReadSeeker {
	cur, _ := rs.Seek(0, io.SeekCurrent)
	return &bufferedReadSeeker{
		rs:     rs,
		buf:    make([]byte, 64<<10),
		pos:    cur,
		bufPos: cur,
	}
}

func (b *bufferedReadSeeker) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	// Serve from buffer when the logical position lies inside it.
	if b.pos >= b.bufPos && b.pos < b.bufPos+int64(b.bufLen) {
		off := int(b.pos - b.bufPos)
		n := copy(p, b.buf[off:b.bufLen])
		b.pos += int64(n)
		return n, nil
	}
	// Large reads bypass the buffer to avoid extra copying.
	if len(p) >= len(b.buf) {
		if _, err := b.rs.Seek(b.pos, io.SeekStart); err != nil {
			return 0, err
		}
		n, err := b.rs.Read(p)
		b.pos += int64(n)
		b.bufLen = 0
		return n, err
	}
	// Refill buffer at b.pos.
	if _, err := b.rs.Seek(b.pos, io.SeekStart); err != nil {
		return 0, err
	}
	n, err := b.rs.Read(b.buf)
	b.bufPos = b.pos
	b.bufLen = n
	if n == 0 {
		return 0, err
	}
	copied := copy(p, b.buf[:n])
	b.pos += int64(copied)
	// Defer EOF until all buffered bytes are consumed.
	if copied < n && errors.Is(err, io.EOF) {
		err = nil
	}
	return copied, err
}

func (b *bufferedReadSeeker) Seek(offset int64, whence int) (int64, error) {
	var target int64
	switch whence {
	case io.SeekStart:
		target = offset
	case io.SeekCurrent:
		target = b.pos + offset
	case io.SeekEnd:
		end, err := b.rs.Seek(offset, io.SeekEnd)
		if err != nil {
			return 0, err
		}
		b.pos = end
		b.bufLen = 0
		return b.pos, nil
	default:
		return 0, fmt.Errorf("player: invalid seek whence %d", whence)
	}
	if target < 0 {
		return 0, fmt.Errorf("player: negative seek offset %d", target)
	}
	// Keep the buffer when seeking within its already-read range.
	if target >= b.bufPos && target <= b.bufPos+int64(b.bufLen) {
		b.pos = target
		return b.pos, nil
	}
	pos, err := b.rs.Seek(target, io.SeekStart)
	if err != nil {
		return 0, err
	}
	b.pos = pos
	b.bufLen = 0
	return pos, nil
}

// openDecoder sniffs a stream's container by magic bytes and returns a decoder.
// File extensions are never trusted because the CDN occasionally serves MP3
// bytes under a .flac URL (and vice versa).
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
		// Some FLAC files carry a prepended ID3v2 tag; sniff past it if needed.
		if isID3WrappedFLAC(r) {
			return newFLACDecoder(r)
		}
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

// isID3WrappedFLAC checks whether an ID3v2 header is immediately followed by
// the "fLaC" magic bytes, and always rewinds r to offset 0.
func isID3WrappedFLAC(r io.ReadSeeker) bool {
	defer r.Seek(0, io.SeekStart)
	var hdr [10]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return false
	}
	if string(hdr[:3]) != "ID3" {
		return false
	}
	size := int64(hdr[6]&0x7f)<<21 | int64(hdr[7]&0x7f)<<14 | int64(hdr[8]&0x7f)<<7 | int64(hdr[9]&0x7f)
	if _, err := r.Seek(10+size, io.SeekStart); err != nil {
		return false
	}
	var sig [4]byte
	if _, err := io.ReadFull(r, sig[:]); err != nil {
		return false
	}
	return string(sig[:]) == "fLaC"
}

// peek reads up to n bytes and rewinds r to offset 0.
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

// ----- MP3 -----

type mp3Decoder struct {
	src      io.ReadSeeker
	dec      *mp3.Decoder
	seekable bool
	format   Format
	// rem holds 0..3 leftover PCM bytes when go-mp3 returns a count not
	// divisible by FrameSize. Preserving them keeps 16-bit stereo frame
	// alignment intact across Read calls (dropping them turns audio into static).
	rem    [4]byte
	remLen int
}

func newMP3Decoder(r io.ReadSeeker) (decoder, error) {
	if _, err := r.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	var (
		dec      *mp3.Decoder
		err      error
		seekable bool
	)
	if isCompleteSource(r) {
		dec, err = mp3.NewDecoder(newBufferedReadSeeker(r))
		seekable = err == nil
	} else {
		// Growing stream: hide io.Seeker so decoding starts on the first frame
		// instead of waiting for the entire file to finish downloading.
		dec, err = mp3.NewDecoder(bufio.NewReaderSize(nonSeeker{r}, 64<<10))
	}
	if err != nil {
		return nil, fmt.Errorf("player: mp3: %w", err)
	}
	rate := dec.SampleRate()
	if rate <= 0 {
		rate = 44100
	}
	// go-mp3 always decodes to interleaved 16-bit signed LE stereo (4 bytes/frame).
	return &mp3Decoder{
		src:      r,
		dec:      dec,
		seekable: seekable,
		format:   Format{SampleRate: rate, Channels: 2},
	}, nil
}

func (d *mp3Decoder) Format() Format { return d.format }

func (d *mp3Decoder) Duration() time.Duration {
	if !d.seekable {
		return 0
	}
	length := d.dec.Length()
	if length <= 0 || d.format.SampleRate <= 0 {
		return 0
	}
	return framesToDuration(length/int64(d.format.FrameSize()), d.format.SampleRate)
}

// SeekSample moves an MP3 decoder to a frame-accurate sample offset using
// go-mp3's built-in frame index (which also warms up the previous frame's bit
// reservoir so seeks are glitch-free).
func (d *mp3Decoder) SeekSample(sample int64) error {
	if sample < 0 {
		sample = 0
	}
	if !d.seekable {
		// If a streamed download has now finished, upgrade in place to a
		// seekable decoder without touching the reader until ready.
		if !isCompleteSource(d.src) {
			return errSeekUnavailable
		}
		if _, err := d.src.Seek(0, io.SeekStart); err != nil {
			return err
		}
		dec, err := mp3.NewDecoder(newBufferedReadSeeker(d.src))
		if err != nil {
			return err
		}
		d.dec = dec
		d.seekable = true
	}
	frameSize := int64(d.format.FrameSize())
	targetByte := sample * frameSize
	if total := d.dec.Length(); total > 0 && targetByte >= total {
		// Clamp to the final frame so seeking to or past the end ends cleanly.
		targetByte = max(total-frameSize, 0)
	}
	targetByte -= targetByte % frameSize
	if _, err := d.dec.Seek(targetByte, io.SeekStart); err != nil {
		return err
	}
	d.remLen = 0
	return nil
}

// Read returns whole 4-byte PCM stereo frames without ever losing byte
// alignment across calls.
func (d *mp3Decoder) Read(p []byte) (int, error) {
	frameSize := d.format.FrameSize()
	p = p[:len(p)-len(p)%frameSize]
	if len(p) == 0 {
		return 0, nil
	}
	written := 0
	if d.remLen > 0 {
		written = copy(p, d.rem[:d.remLen])
		d.remLen = 0
	}
	for written < frameSize {
		n, err := d.dec.Read(p[written:])
		written += n
		if err != nil {
			aligned := written - written%frameSize
			if aligned > 0 {
				// Save any stray bytes, though on EOF they cannot form a frame.
				if !errors.Is(err, io.EOF) {
					d.remLen = copy(d.rem[:], p[aligned:written])
				}
				return aligned, nil
			}
			return 0, err
		}
	}
	aligned := written - written%frameSize
	if rem := written - aligned; rem > 0 {
		d.remLen = copy(d.rem[:], p[aligned:written])
	}
	return aligned, nil
}

// ----- FLAC -----

type flacDecoder struct {
	src     io.ReadSeeker
	stream  *flac.Stream
	format  Format
	backing []byte
	buf     []byte
}

func newFLACDecoder(r io.ReadSeeker) (decoder, error) {
	if _, err := r.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	stream, err := flac.NewSeek(r)
	if err != nil {
		return nil, fmt.Errorf("player: flac: %w", err)
	}
	rate := int(stream.Info.SampleRate)
	if rate <= 0 {
		rate = 44100
	}
	channels := int(stream.Info.NChannels)
	if channels < 1 {
		channels = 2
	}
	if channels > 2 {
		// Multi-channel FLAC (e.g. 5.1 surround) is downmixed cleanly to stereo.
		channels = 2
	}
	return &flacDecoder{
		src:    r,
		stream: stream,
		format: Format{SampleRate: rate, Channels: channels},
	}, nil
}

func (d *flacDecoder) Format() Format { return d.format }

func (d *flacDecoder) Duration() time.Duration {
	if d.stream.Info.NSamples == 0 || d.format.SampleRate <= 0 {
		return 0
	}
	return framesToDuration(int64(d.stream.Info.NSamples), d.format.SampleRate)
}

// SeekSample jumps to sample using the FLAC seek table (when the source is
// complete) and trims the exact intra-frame offset so the landing position is
// sample-accurate.
func (d *flacDecoder) SeekSample(sample int64) error {
	if !isCompleteSource(d.src) {
		return errSeekUnavailable
	}
	if sample < 0 {
		sample = 0
	}
	if total := int64(d.stream.Info.NSamples); total > 0 && sample >= total {
		sample = total - 1
	}
	frameStart, err := d.stream.Seek(uint64(sample))
	if err != nil {
		return err
	}
	d.buf = d.buf[:0]
	if skip := sample - int64(frameStart); skip > 0 {
		return discardForwardSamples(d, skip, d.format)
	}
	return nil
}

// Read decodes FLAC frames into p until p is full or the stream ends.
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

// fill decodes one FLAC frame into d.buf as interleaved s16le PCM.
//
// IMPORTANT: github.com/mewkiz/flac's Stream.ParseNext() already calls
// frame.Correlate() inside frame.Frame.Parse(). Calling f.Correlate() a second
// time corrupts every mid-side/left-side/right-side stereo frame into severe
// clipping and frame-boundary crackling noise.
func (d *flacDecoder) fill() (bool, error) {
	f, err := d.stream.ParseNext()
	if err != nil {
		if errors.Is(err, io.EOF) {
			return false, nil
		}
		return false, err
	}
	srcChannels := len(f.Subframes)
	if srcChannels == 0 {
		return true, nil
	}
	count := len(f.Subframes[0].Samples)
	if count == 0 {
		return true, nil
	}
	bps := int(f.BitsPerSample)
	if bps == 0 {
		bps = int(d.stream.Info.BitsPerSample)
	}
	if bps <= 0 {
		bps = 16
	}

	outChannels := d.format.Channels
	need := count * outChannels * 2
	if cap(d.backing) < need {
		d.backing = make([]byte, need)
	}
	out := d.backing[:need]

	if srcChannels == 1 && outChannels == 1 {
		s0 := f.Subframes[0].Samples
		for i := 0; i < count; i++ {
			binary.LittleEndian.PutUint16(out[i*2:], uint16(scaleToInt16(s0[i], bps)))
		}
	} else if srcChannels == 1 && outChannels == 2 {
		s0 := f.Subframes[0].Samples
		for i := 0; i < count; i++ {
			v := uint16(scaleToInt16(s0[i], bps))
			off := i * 4
			binary.LittleEndian.PutUint16(out[off:], v)
			binary.LittleEndian.PutUint16(out[off+2:], v)
		}
	} else if srcChannels == 2 && outChannels == 2 {
		s0 := f.Subframes[0].Samples
		s1 := f.Subframes[1].Samples
		for i := 0; i < count; i++ {
			off := i * 4
			binary.LittleEndian.PutUint16(out[off:], uint16(scaleToInt16(s0[i], bps)))
			binary.LittleEndian.PutUint16(out[off+2:], uint16(scaleToInt16(s1[i], bps)))
		}
	} else {
		// Multi-channel FLAC (3..8 channels in SMPTE/ITU-R order) downmixed to
		// stereo so center vocals and surround channels are preserved cleanly.
		for i := 0; i < count; i++ {
			left, right := downmixFLACSample(f.Subframes, srcChannels, i)
			off := i * 4
			binary.LittleEndian.PutUint16(out[off:], uint16(scaleFloatToInt16(left, bps)))
			binary.LittleEndian.PutUint16(out[off+2:], uint16(scaleFloatToInt16(right, bps)))
		}
	}

	d.buf = out
	return true, nil
}

// scaleToInt16 converts a signed bps-bit FLAC sample into signed 16-bit PCM
// using round-to-nearest when down-converting (e.g. 24-bit Hi-Res to 16-bit)
// and left-shifting when up-converting (< 16-bit).
func scaleToInt16(sample int32, bps int) int16 {
	var v int64
	switch {
	case bps == 16:
		v = int64(sample)
	case bps > 16:
		shift := uint(bps - 16)
		// Half-LSB rounding removes truncation DC bias and lowers quantization error.
		v = (int64(sample) + (1 << (shift - 1))) >> shift
	default:
		v = int64(sample) << uint(16-bps)
	}
	return clampInt64ToInt16(v)
}

func scaleFloatToInt16(sample float64, bps int) int16 {
	switch {
	case bps > 16:
		sample /= float64(int64(1) << uint(bps-16))
	case bps < 16:
		sample *= float64(int64(1) << uint(16-bps))
	}
	return clampInt64ToInt16(int64(math.Round(sample)))
}

// downmixFLACSample downmixes a multi-channel FLAC sample (SMPTE/ITU-R layout)
// to normalized stereo (L, R) so center vocals and surround channels are
// preserved without clipping.
func downmixFLACSample(subframes []*flacframe.Subframe, channels, idx int) (float64, float64) {
	const (
		invSqrt2 = 0.7071067811865476
		half     = 0.5
	)
	at := func(ch int) float64 {
		if ch >= len(subframes) || idx >= len(subframes[ch].Samples) {
			return 0
		}
		return float64(subframes[ch].Samples[idx])
	}
	l := at(0)
	r := at(1)
	switch channels {
	case 3: // L, R, C
		c := at(2) * invSqrt2
		return (l + c) / (1 + invSqrt2), (r + c) / (1 + invSqrt2)
	case 4: // L, R, Ls, Rs
		return (l + at(2)*invSqrt2) / (1 + invSqrt2), (r + at(3)*invSqrt2) / (1 + invSqrt2)
	case 5: // L, R, C, Ls, Rs
		c := at(2) * invSqrt2
		const norm = 1 + 2*invSqrt2
		return (l + c + at(3)*invSqrt2) / norm, (r + c + at(4)*invSqrt2) / norm
	case 6: // 5.1: L, R, C, LFE, Ls, Rs
		c := at(2) * invSqrt2
		lfe := at(3) * half
		const norm = 1 + 2*invSqrt2 + half
		return (l + c + lfe + at(4)*invSqrt2) / norm, (r + c + lfe + at(5)*invSqrt2) / norm
	case 7: // 6.1: L, R, C, LFE, Cs, Sl, Sr
		c := at(2) * invSqrt2
		lfe := at(3) * half
		cs := at(4) * half
		const norm = 1 + 2*invSqrt2 + 2*half
		return (l + c + lfe + cs + at(5)*invSqrt2) / norm, (r + c + lfe + cs + at(6)*invSqrt2) / norm
	default: // 7.1 (8ch): L, R, C, LFE, Ls, Rs, Sl, Sr
		c := at(2) * invSqrt2
		lfe := at(3) * half
		const norm = 1 + 3*invSqrt2 + half
		return (l + c + lfe + at(4)*invSqrt2 + at(6)*invSqrt2) / norm,
			(r + c + lfe + at(5)*invSqrt2 + at(7)*invSqrt2) / norm
	}
}

// ----- WAV -----
//
// WAV decoding covers test fixtures and PCM tracks stored on NetEase Cloud Disk
// (8-bit unsigned, 16-bit signed LE, and 24-bit signed LE).

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
			audioFormat := binary.LittleEndian.Uint16(body[0:])
			channels := int(binary.LittleEndian.Uint16(body[2:]))
			rate := int(binary.LittleEndian.Uint32(body[4:]))
			bits := int(binary.LittleEndian.Uint16(body[14:]))
			if audioFormat != 1 || (bits != 8 && bits != 16 && bits != 24) || channels < 1 || channels > 2 {
				return nil, fmt.Errorf("%w: wav pcm %d-bit %d channels", ErrUnsupportedFormat, bits, channels)
			}
			d.format = Format{SampleRate: rate, Channels: channels}
			d.bits = bits
			if pad != 0 {
				if _, err := r.Seek(pad, io.SeekCurrent); err != nil {
					return nil, err
				}
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

func (d *wavDecoder) srcFrameSize() int {
	return d.format.Channels * (d.bits / 8)
}

func (d *wavDecoder) Format() Format { return d.format }

func (d *wavDecoder) Duration() time.Duration {
	srcBPS := int64(d.format.SampleRate) * int64(d.srcFrameSize())
	if srcBPS <= 0 || d.end <= d.start {
		return 0
	}
	frames := (d.end - d.start) / int64(d.srcFrameSize())
	return framesToDuration(frames, d.format.SampleRate)
}

func (d *wavDecoder) SeekSample(sample int64) error {
	if sample < 0 {
		sample = 0
	}
	offset := d.start + sample*int64(d.srcFrameSize())
	if offset < d.start {
		offset = d.start
	}
	if d.end > 0 && offset >= d.end {
		offset = d.end
	}
	_, err := d.src.Seek(offset, io.SeekStart)
	return err
}

func (d *wavDecoder) Read(p []byte) (int, error) {
	outFrame := d.format.FrameSize()
	p = p[:len(p)-len(p)%outFrame]
	if len(p) == 0 {
		return 0, nil
	}
	pos, err := d.src.Seek(0, io.SeekCurrent)
	if err != nil {
		return 0, err
	}
	if pos >= d.end {
		return 0, io.EOF
	}
	remaining := d.end - pos

	switch d.bits {
	case 8:
		srcFrame := d.srcFrameSize()
		maxFrames := min(int64(len(p)/outFrame), remaining/int64(srcFrame))
		if maxFrames <= 0 {
			return 0, io.EOF
		}
		need := int(maxFrames) * srcFrame
		if cap(d.scratch) < need {
			d.scratch = make([]byte, need)
		}
		n, rerr := d.src.Read(d.scratch[:need])
		n -= n % srcFrame
		for i := 0; i < n; i++ {
			sample := int16(int32(d.scratch[i])*256 - 32768)
			binary.LittleEndian.PutUint16(p[i*2:], uint16(sample))
		}
		if n > 0 && errors.Is(rerr, io.EOF) {
			rerr = nil
		}
		return n * 2, rerr

	case 24:
		srcFrame := d.srcFrameSize()
		maxFrames := min(int64(len(p)/outFrame), remaining/int64(srcFrame))
		if maxFrames <= 0 {
			return 0, io.EOF
		}
		need := int(maxFrames) * srcFrame
		if cap(d.scratch) < need {
			d.scratch = make([]byte, need)
		}
		n, rerr := d.src.Read(d.scratch[:need])
		n -= n % srcFrame
		samples := n / 3
		for i := 0; i < samples; i++ {
			b0 := int32(d.scratch[i*3])
			b1 := int32(d.scratch[i*3+1])
			b2 := int32(int8(d.scratch[i*3+2])) // sign-extend MSB
			raw := b0 | b1<<8 | b2<<16
			binary.LittleEndian.PutUint16(p[i*2:], uint16(scaleToInt16(raw, 24)))
		}
		if samples > 0 && errors.Is(rerr, io.EOF) {
			rerr = nil
		}
		return samples * 2, rerr

	default: // 16-bit
		if int64(len(p)) > remaining {
			p = p[:remaining]
			p = p[:len(p)-len(p)%outFrame]
		}
		if len(p) == 0 {
			return 0, io.EOF
		}
		n, rerr := d.src.Read(p)
		n -= n % outFrame
		if n > 0 && errors.Is(rerr, io.EOF) {
			rerr = nil
		}
		return n, rerr
	}
}

func clampInt64ToInt16(v int64) int16 {
	if v > 32767 {
		return 32767
	}
	if v < -32768 {
		return -32768
	}
	return int16(v)
}
