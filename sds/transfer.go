package sds

import "math"

// SendOp handles the creation of messages to transfer a waveform.
type SendOp struct {
	length   int
	bitDepth int
	samples  []int
	data     DataPacket
	num      byte
}

func NewSendOp(samples []int, h *DumpHeader) *SendOp {
	h.Length = uint(len(samples))

	s := &SendOp{
		length:   len(samples),
		bitDepth: int(h.BitDepth),
		samples:  samples,
	}
	s.data.Channel = h.Channel
	return s
}

// Done returns true when the complete waveform has been sent.
func (s *SendOp) Done() bool {
	return len(s.samples) == 0
}

// Progress returns the percentage of completion.
func (s *SendOp) Progress() int {
	done := s.length - len(s.samples)
	return int(math.Round((float64(done) / float64(s.length)) * 100))
}

// NextMessage returns the next message to be sent.
func (s *SendOp) NextMessage() Message {
	if s.Done() {
		return nil
	}

	// Prepare next data packet.
	s.samples = s.data.SetSamples(s.samples, int(s.bitDepth))
	s.data.PacketNumber = s.nextNumber()
	s.data.Checksum = s.data.ComputeChecksum()
	return &s.data
}

func (s *SendOp) nextNumber() byte {
	n := s.num
	if n >= 127 {
		s.num = 0
	} else {
		s.num = n + 1
	}
	return n
}
