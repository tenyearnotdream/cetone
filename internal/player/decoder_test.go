package player

import (
	"bytes"
	"context"
	"encoding/binary"
	"io"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mewkiz/flac"
	flacframe "github.com/mewkiz/flac/frame"
	"github.com/mewkiz/flac/meta"
)

// encodeTestFLAC creates a real FLAC file on disk with the requested channel
// assignment, bit depth, sample rate, and per-channel sample arrays.
func encodeTestFLAC(t *testing.T, path string, sampleRate, bps int, chAssign flacframe.Channels, channels [][]int32, blockSize int) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	nch := len(channels)
	total := len(channels[0])
	info := &meta.StreamInfo{
		BlockSizeMin:  uint16(blockSize),
		BlockSizeMax:  uint16(blockSize),
		SampleRate:    uint32(sampleRate),
		NChannels:     uint8(nch),
		BitsPerSample: uint8(bps),
		NSamples:      uint64(total),
	}
	enc, err := flac.NewEncoder(f, info)
	if err != nil {
		t.Fatal(err)
	}
	enc.EnablePredictionAnalysis(false)

	for start := 0; start < total; start += blockSize {
		end := min(start+blockSize, total)
		n := end - start
		subframes := make([]*flacframe.Subframe, nch)
		for ch := 0; ch < nch; ch++ {
			samples := make([]int32, n)
			copy(samples, channels[ch][start:end])
			subframes[ch] = &flacframe.Subframe{
				SubHeader: flacframe.SubHeader{Pred: flacframe.PredVerbatim},
				Samples:   samples,
				NSamples:  n,
			}
		}
		fr := &flacframe.Frame{
			Header: flacframe.Header{
				HasFixedBlockSize: true,
				BlockSize:         uint16(n),
				SampleRate:        uint32(sampleRate),
				Channels:          chAssign,
				BitsPerSample:     uint8(bps),
			},
			Subframes: subframes,
		}
		if err := enc.WriteFrame(fr); err != nil {
			t.Fatalf("WriteFrame at %d: %v", start, err)
		}
	}
	if err := enc.Close(); err != nil {
		t.Fatal(err)
	}
}

func decodeAllPCM(t *testing.T, dec decoder) []int16 {
	t.Helper()
	var raw []byte
	buf := make([]byte, 4096)
	for {
		n, err := dec.Read(buf)
		if n > 0 {
			raw = append(raw, buf[:n]...)
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("decoder.Read: %v", err)
		}
	}
	samples := make([]int16, len(raw)/2)
	for i := range samples {
		samples[i] = int16(binary.LittleEndian.Uint16(raw[i*2:]))
	}
	return samples
}

// TestFLACDecodeStereoDecorrelationAccuracy guards against the double-Correlate
// bug that previously turned Mid-Side, Left-Side, and Right-Side stereo FLAC
// frames into harsh clipping and static crackling noise.
func TestFLACDecodeStereoDecorrelationAccuracy(t *testing.T) {
	modes := []struct {
		name   string
		assign flacframe.Channels
	}{
		{"LR", flacframe.ChannelsLR},
		{"LeftSide", flacframe.ChannelsLeftSide},
		{"SideRight", flacframe.ChannelsSideRight},
		{"MidSide", flacframe.ChannelsMidSide},
	}
	for _, bps := range []int{16, 24} {
		for _, mode := range modes {
			name := mode.name + "_" + string(rune('0'+bps/10)) + string(rune('0'+bps%10)) + "bit"
			t.Run(name, func(t *testing.T) {
				const (
					sampleRate = 44100
					nSamples   = 4096
					blockSize  = 1024
				)
				amp := float64(int64(1)<<(bps-2)) * 0.75
				left := make([]int32, nSamples)
				right := make([]int32, nSamples)
				for i := 0; i < nSamples; i++ {
					tSec := float64(i) / sampleRate
					left[i] = int32(math.Round(amp * math.Sin(2*math.Pi*440*tSec)))
					right[i] = int32(math.Round(amp * math.Cos(2*math.Pi*660*tSec)))
				}
				path := filepath.Join(t.TempDir(), name+".flac")
				encodeTestFLAC(t, path, sampleRate, bps, mode.assign, [][]int32{left, right}, blockSize)

				f, err := os.Open(path)
				if err != nil {
					t.Fatal(err)
				}
				defer f.Close()

				dec, err := openDecoder(f)
				if err != nil {
					t.Fatalf("openDecoder: %v", err)
				}
				if got := dec.Format(); got.SampleRate != sampleRate || got.Channels != 2 {
					t.Fatalf("format = %+v, want 44100Hz 2ch", got)
				}
				pcm := decodeAllPCM(t, dec)
				if len(pcm) != nSamples*2 {
					t.Fatalf("decoded %d int16 samples, want %d", len(pcm), nSamples*2)
				}
				maxDiff := int32(0)
				for i := 0; i < nSamples; i++ {
					wantL := scaleToInt16(left[i], bps)
					wantR := scaleToInt16(right[i], bps)
					gotL := pcm[i*2]
					gotR := pcm[i*2+1]
					diffL := int32(gotL) - int32(wantL)
					if diffL < 0 {
						diffL = -diffL
					}
					diffR := int32(gotR) - int32(wantR)
					if diffR < 0 {
						diffR = -diffR
					}
					if diffL > maxDiff {
						maxDiff = diffL
					}
					if diffR > maxDiff {
						maxDiff = diffR
					}
				}
				if maxDiff > 0 {
					t.Fatalf("decoded waveform diverged from reference by max %d LSB (want 0)", maxDiff)
				}
			})
		}
	}
}

func TestFLACSeekSampleAccuracy(t *testing.T) {
	const (
		sampleRate = 44100
		nSamples   = 8192
		blockSize  = 1024
	)
	left := make([]int32, nSamples)
	right := make([]int32, nSamples)
	for i := 0; i < nSamples; i++ {
		left[i] = int32(i)
		right[i] = int32(-i)
	}
	path := filepath.Join(t.TempDir(), "ramp.flac")
	encodeTestFLAC(t, path, sampleRate, 16, flacframe.ChannelsMidSide, [][]int32{left, right}, blockSize)

	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	dec, err := openDecoder(f)
	if err != nil {
		t.Fatal(err)
	}
	s, ok := dec.(seeker)
	if !ok {
		t.Fatal("FLAC decoder does not implement seeker")
	}
	// Seek into the middle of frame 3 (not on a block boundary).
	const target = 3500
	if err := s.SeekSample(target); err != nil {
		t.Fatalf("SeekSample(%d): %v", target, err)
	}
	buf := make([]byte, 16)
	n, err := dec.Read(buf)
	if err != nil || n < 4 {
		t.Fatalf("Read after seek: n=%d err=%v", n, err)
	}
	gotL := int16(binary.LittleEndian.Uint16(buf[0:2]))
	gotR := int16(binary.LittleEndian.Uint16(buf[2:4]))
	if gotL != target || gotR != -target {
		t.Fatalf("after SeekSample(%d): got (%d, %d), want (%d, %d)", target, gotL, gotR, target, -target)
	}
}

func TestFLACMultiChannelDownmix(t *testing.T) {
	const (
		sampleRate = 48000
		nSamples   = 1024
		blockSize  = 1024
	)
	// 5.1 layout: L, R, C, LFE, Ls, Rs — put vocal energy solely on Center (ch 2).
	channels := make([][]int32, 6)
	for ch := range channels {
		channels[ch] = make([]int32, nSamples)
	}
	for i := 0; i < nSamples; i++ {
		channels[2][i] = 20000
	}
	path := filepath.Join(t.TempDir(), "surround.flac")
	encodeTestFLAC(t, path, sampleRate, 16, flacframe.Channels(5), channels, blockSize)

	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	dec, err := openDecoder(f)
	if err != nil {
		t.Fatal(err)
	}
	if got := dec.Format().Channels; got != 2 {
		t.Fatalf("5.1 FLAC channels = %d, want 2 (stereo downmix)", got)
	}
	pcm := decodeAllPCM(t, dec)
	if len(pcm) != nSamples*2 {
		t.Fatalf("decoded %d samples, want %d", len(pcm), nSamples*2)
	}
	if pcm[0] <= 0 || pcm[0] != pcm[1] {
		t.Fatalf("center channel downmix produced L=%d R=%d, want equal positive values", pcm[0], pcm[1])
	}
}

func TestBufferedReadSeeker(t *testing.T) {
	data := make([]byte, 200_000)
	for i := range data {
		data[i] = byte((i*31 + 7) & 0xff)
	}
	brs := newBufferedReadSeeker(bytes.NewReader(data))

	// Sequential small reads crossing buffer boundaries.
	var got []byte
	tmp := make([]byte, 137)
	for {
		n, err := brs.Read(tmp)
		if n > 0 {
			got = append(got, tmp[:n]...)
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("Read: %v", err)
		}
	}
	if !bytes.Equal(got, data) {
		t.Fatal("sequential read through bufferedReadSeeker corrupted bytes")
	}

	// Random seeks (start, current, end).
	for _, offset := range []int64{0, 123, 65535, 65536, 150000, 199990} {
		pos, err := brs.Seek(offset, io.SeekStart)
		if err != nil || pos != offset {
			t.Fatalf("Seek(%d): pos=%d err=%v", offset, pos, err)
		}
		buf := make([]byte, 10)
		n, err := io.ReadFull(brs, buf)
		if err != nil || n != 10 {
			t.Fatalf("ReadFull at %d: n=%d err=%v", offset, n, err)
		}
		if !bytes.Equal(buf, data[offset:offset+10]) {
			t.Fatalf("bytes at %d did not match", offset)
		}
	}
	endPos, err := brs.Seek(-8, io.SeekEnd)
	if err != nil || endPos != int64(len(data)-8) {
		t.Fatalf("SeekEnd(-8): pos=%d err=%v", endPos, err)
	}
	tail := make([]byte, 8)
	if _, err := io.ReadFull(brs, tail); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(tail, data[len(data)-8:]) {
		t.Fatal("SeekEnd bytes did not match")
	}
}

func TestApplyGainRampsSmoothlyWithoutStepClicks(t *testing.T) {
	// 400 stereo frames at 44100Hz (~9ms) with constant sample 10000.
	pcm := make([]byte, 400*4)
	for i := 0; i < len(pcm); i += 2 {
		binary.LittleEndian.PutUint16(pcm[i:], uint16(int16(10000)))
	}
	e := NewEngine()
	e.SetVolume(1.0)
	tr := &track{
		engine:      e,
		format:      Format{SampleRate: 44100, Channels: 2},
		currentGain: 0.0,
	}
	tr.applyGain(pcm)
	if math.Abs(tr.currentGain-1.0) > 0.01 {
		t.Fatalf("final gain = %v, want ~1.0", tr.currentGain)
	}
	first := int16(binary.LittleEndian.Uint16(pcm[0:2]))
	last := int16(binary.LittleEndian.Uint16(pcm[len(pcm)-4 : len(pcm)-2]))
	if first > 100 {
		t.Fatalf("first sample jumped to %d on fade-in (would click)", first)
	}
	if last < 9900 {
		t.Fatalf("last sample only reached %d after fade-in", last)
	}
	// Verify monotonicity (no zipper step > 40 LSB per sample).
	prev := int16(0)
	for i := 0; i < len(pcm); i += 4 {
		cur := int16(binary.LittleEndian.Uint16(pcm[i : i+2]))
		if cur < prev || cur-prev > 40 {
			t.Fatalf("non-smooth ramp at frame %d: %d -> %d", i/4, prev, cur)
		}
		prev = cur
	}
}

func TestEnginePrepareAndPlayAtResetProgressCleanly(t *testing.T) {
	dir := t.TempDir()
	pathA := filepath.Join(dir, "a.wav")
	pathB := filepath.Join(dir, "b.wav")
	writeWAV(t, pathA, 8000, 2, 4)
	writeWAV(t, pathB, 8000, 2, 4)

	e, _ := newTestEngine(t)
	if err := e.Play(context.Background(), Source{SongID: 1, Title: "A", File: pathA, Duration: 4 * time.Second}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(250 * time.Millisecond)
	if snap := e.Snapshot(); snap.SongID != 1 || snap.Position < 80*time.Millisecond {
		t.Fatalf("track A did not advance: %+v", snap)
	}

	// Prepare immediately stops track A and resets position & metadata to track B.
	e.Prepare(2, "B", 3*time.Minute, 0)
	snap := e.Snapshot()
	if snap.SongID != 2 || snap.Title != "B" || snap.Status != StatusBuffering || snap.Position != 0 || snap.Duration != 3*time.Minute {
		t.Fatalf("Prepare left stale state: %+v", snap)
	}

	// PlayAt starts track B directly at 2s without flashing 0:00.
	if err := e.PlayAt(context.Background(), Source{SongID: 2, Title: "B", File: pathB, Duration: 4 * time.Second}, 2*time.Second); err != nil {
		t.Fatal(err)
	}
	snap = e.Snapshot()
	if snap.SongID != 2 || snap.Position < 2*time.Second || snap.Position > 2300*time.Millisecond {
		t.Fatalf("PlayAt(2s) started at %+v", snap)
	}
}
