package sds

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"io/ioutil"
	mrand "math/rand"
	"os"
	"reflect"
	"testing"

	"github.com/go-audio/audio"
	"github.com/go-audio/wav"
)

func Test20bit(t *testing.T) {
	tests := []struct {
		num    uint
		result []byte
	}{
		{0x0000, []byte{0x00, 0x00, 0x00}},
		{0x0001, []byte{0x01, 0x00, 0x00}},
		{0x3FFF, []byte{0x7F, 0x7F, 0x00}},
		{0x4000, []byte{0x00, 0x00, 0x01}},
		{0x5894, []byte{0x14, 0x31, 0x01}},
		{0x5DBF, []byte{0x3F, 0x3B, 0x01}},
	}

	for _, test := range tests {
		e := append20bit(nil, test.num)
		if !bytes.Equal(e, test.result) {
			t.Fatalf("enc %#x = %x, want %x", test.num, e, test.result)
		}
		if n := dec20bit(e[0], e[1], e[2]); n != test.num {
			t.Fatalf("dec %x = %x, want %x", e, n, test.num)
		}
	}
}

func TestMessageEncoding(t *testing.T) {
	tests := []Message{
		&DumpHeader{1, 2, 16, 4, 5, 6, 7, 8},
		&DumpRequest{1, 2},
		&DataPacket{1, 2, [120]byte{3, 4, 5, 6}, 7},
		&ControlPacket{Ack, 1, 8},
	}

	for _, msg := range tests {
		enc := msg.Encode(nil)
		// t.Logf("%T: %x", msg, enc)
		dec, err := Decode(enc)
		if err != nil {
			t.Fatal("decode error:", err)
		}
		if !reflect.DeepEqual(dec, msg) {
			t.Fatalf("wrong decoded message: %#v", dec)
		}
	}
}

func TestSamples(t *testing.T) {
	tests := []struct {
		name   string
		bits   int
		rate   int
		length int
	}{
		{name: "akwf1_24bit_44k", bits: 24, rate: 44100, length: 600},
		{name: "akwf1_16bit_44k", bits: 16, rate: 44100, length: 600},
		{name: "akwf1_8bit_44k", bits: 8, rate: 44100, length: 600},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			sdsFile, err := loadSDS(fmt.Sprintf("testdata/%s.sds", test.name))
			if err != nil {
				t.Fatal(err)
			}

			// Check header values.
			h := sdsFile.header
			if h.BitDepth != byte(test.bits) {
				t.Error("SDS header has wrong bit depth", h.BitDepth)
			}
			if h.Period != samplerateToPeriod(test.rate) {
				t.Error("SDS header has wrong period", h.Period)
			}
			if h.Length != uint(test.length) {
				t.Error("SDS header has wrong length", h.Length)
			}

			// Check samples against .wav source.
			wavFile, err := loadWAV(fmt.Sprintf("testdata/%s.wav", test.name))
			if err != nil {
				t.Fatal(err)
			}
			if !samplesEqual(sdsFile.samples[:h.Length], wavFile.samples) {
				t.Error("samples not equal")
				t.Logf("wav (%d) %8d", len(wavFile.samples), wavFile.samples)
				t.Logf("sds (%d) %8d", len(sdsFile.samples), sdsFile.samples)
			}

			// Re-encode and check against .sds.
			encoded := encodeSDS(wavFile.samples, test.rate, test.bits)
			if !bytes.Equal(encoded, sdsFile.raw) {
				t.Error("re-encoded SDS bytes mismatch")
				t.Logf(" got: %x", encoded)
				t.Logf("want: %x", sdsFile.raw)

			}
		})
	}
}

func TestSetSamplesLengths(t *testing.T) {
	var msg DataPacket
	mrand.Read(msg.Data[:])

	// Case where sample slice is shorter than msg.Data.
	samples1 := []int{-127, -126, -125}
	rem1 := msg.SetSamples(samples1, 8)
	if len(rem1) != 0 {
		t.Fatalf("SetSamples([%d], %d bits) -> %d, want %d", len(samples1), 8, len(rem1), 0)
	}
	if msg.Data != ([120]byte{0, 64, 1, 0, 1, 64}) {
		t.Fatalf("wrong data: %d", msg.Data)
	}

	// Case where sample slice is longer than msg.Data.
	samples2 := make([]int, 90)
	for i := range samples2 {
		samples2[i] = i - 127
	}
	rem2 := msg.SetSamples(samples2, 8)
	if len(rem2) != 30 {
		t.Fatalf("SetSamples([%d], %d bits) -> %d, want %d", len(samples2), 8, len(rem2), 30)
	}
	wantData := [120]byte{
		0, 64, 1, 0, 1, 64, 2, 0, 2, 64, 3, 0, 3, 64, 4, 0,
		4, 64, 5, 0, 5, 64, 6, 0, 6, 64, 7, 0, 7, 64, 8, 0,
		8, 64, 9, 0, 9, 64, 10, 0, 10, 64, 11, 0, 11, 64, 12, 0,
		12, 64, 13, 0, 13, 64, 14, 0, 14, 64, 15, 0, 15, 64, 16, 0,
		16, 64, 17, 0, 17, 64, 18, 0, 18, 64, 19, 0, 19, 64, 20, 0,
		20, 64, 21, 0, 21, 64, 22, 0, 22, 64, 23, 0, 23, 64, 24, 0,
		24, 64, 25, 0, 25, 64, 26, 0, 26, 64, 27, 0, 27, 64, 28, 0,
		28, 64, 29, 0, 29, 64, 30, 0,
	}
	if msg.Data != wantData {
		t.Fatalf("wrong data: %d", msg.Data)
	}
}

func encodeSDS(samples []int, sampleRate, bitDepth int) []byte {
	h := &DumpHeader{BitDepth: byte(bitDepth), Period: samplerateToPeriod(sampleRate)}
	send := NewSendOp(samples, h)
	output := h.Encode(nil)
	for !send.Done() {
		output = send.NextMessage().Encode(output)
	}
	return output
}

type sdsFile struct {
	header  *DumpHeader
	samples []int  // decoded sample data
	raw     []byte // content of .sds file
}

func loadSDS(file string) (*sdsFile, error) {
	raw, err := ioutil.ReadFile(file)
	if err != nil {
		return nil, err
	}

	r := &sdsFile{raw: raw}
	reader := bufio.NewReader(bytes.NewReader(raw))
	for i := 0; ; i++ {
		msg, err := readMessage(reader)
		if err == io.EOF {
			break
		} else if err != nil {
			return r, fmt.Errorf("at msg %d: %v", i, err)
		}

		switch msg := msg.(type) {
		case *DumpHeader:
			if r.header != nil {
				return r, fmt.Errorf("msg %d: extra header", i)
			}
			r.header = msg
		case *DataPacket:
			if r.header == nil {
				return r, fmt.Errorf("data packet before header")
			}
			if cs := msg.ComputeChecksum(); cs != msg.Checksum {
				return r, fmt.Errorf("msg %d: checksum %x != %x", i, cs, msg.Checksum)
			}
			r.samples = msg.GetSamples(r.samples, int(r.header.BitDepth))
		}
	}
	return r, nil
}

func readMessage(r *bufio.Reader) (Message, error) {
	rawmsg, err := r.ReadBytes(0xF7)
	if err != nil {
		return nil, err
	}
	return Decode(rawmsg)
}

type wavFile struct {
	samples []int
}

func loadWAV(file string) (*wavFile, error) {
	fd, err := os.Open(file)
	if err != nil {
		return nil, err
	}
	defer fd.Close()

	decoder := wav.NewDecoder(fd)
	decoder.ReadInfo()
	if decoder.Err() != nil {
		return nil, err
	}
	if decoder.NumChans != 1 {
		return nil, fmt.Errorf("file has %d channels, want mono", decoder.NumChans)
	}
	r := new(wavFile)
	buf := audio.IntBuffer{Data: make([]int, 128)}
	for {
		n, err := decoder.PCMBuffer(&buf)
		if n == 0 || decoder.EOF() {
			break
		}
		if err != nil {
			return nil, err
		}
		r.samples = append(r.samples, buf.Data[:n]...)
	}

	// Fixup unsigned -> signed for 8-bit .wav to match .sds output.
	if decoder.BitDepth == 8 {
		for i := range r.samples {
			r.samples[i] = r.samples[i] - 128
		}
	}
	return r, nil
}

func samplesEqual(s1, s2 []int) bool {
	if len(s1) != len(s2) {
		return false
	}
	for i := range s1 {
		if s1[i] != s2[i] {
			return false
		}
	}
	return true
}

func samplerateToPeriod(rate int) uint {
	return uint(1000000000 / rate)
}
