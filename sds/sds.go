// Package sds implements the MIDI Sample Dump Standard.
package sds

import (
	"bytes"
	"errors"
	"fmt"
)

// DumpHeader is sent to the receiver to provide information about the waveform data that
// is about to be sent in DataPacket messages.
type DumpHeader struct {
	Channel byte
	Number  uint16 // waveform number (max 16384)

	// Sample format.
	BitDepth byte // bits per sample
	Period   uint // sample period in nanoseconds, i.e. 1.000.000.000/samplerate
	Length   uint // total number of samples in waveform

	// Sustain loop.
	LoopStart uint
	LoopEnd   uint
	LoopType  byte
}

// Loop types.
const (
	LoopForward  = byte(0x00)
	LoopPingPong = byte(0x01)
	LoopNone     = byte(0x7F)
)

// DumpRequest is sent by the receiving device who wishes to initiate the dump.
type DumpRequest struct {
	Channel byte
	Number  uint16 // waveform number (max 16384)
}

// DataPacket is used to transfer the actual waveform data. It transfers 120 bytes of
// waveform data at a time.
type DataPacket struct {
	Channel      byte
	PacketNumber byte
	Data         [120]byte
	Checksum     byte
}

// ControlPacket is sent to control the data transfer.
type ControlPacket struct {
	Type         ControlPacketType
	Channel      byte
	PacketNumber byte
}

type ControlPacketType byte

const (
	// The receiver sends Ack after successfully receiving a DumpHeader and
	// after each successfully received DataPacket. It means "the last message
	// was received correctly. Proceed with the next message".
	//
	// PacketNumber is the packet that was received correctly (0 if responding
	// to a DumpHeader). The transmitter uses this to determine which particular
	// packet the receiver has accepted (in case packet dumps get out of order).
	Ack = ControlPacketType(0x7F)

	// The receiver sends Nak after unsuccessfully receiving a Dump Header and
	// after each unsuccessfully received Data Packet. It means "the last
	// message was not received correctly. Resend that message".
	//
	// PacketNumber is the packet that was received incorrectly (0 if responding
	// to a Dump Header). The transmitter uses this to determine which
	// particular packet the receiver has rejected (in case packet dumps get out
	// of order).
	Nak = ControlPacketType(0x7E)

	// The receiver sends this when it wishes the transmitter to stop the dump.
	//
	// PacketNumber is the packet number upon which the dump is aborted (0 if
	// responding to a Dump Header).
	Cancel = ControlPacketType(0x7D)

	// The receiver sends this when it wants the transmitter to pause the dump
	// operation. The transmitter will send nothing until it receives another
	// message from the receiver; an ACK to continue, a NAK to resend, or a
	// CANCEL to abort the dump. kk is the packet number upon which the wait was
	// initiated (0 if responding to a Dump Header).
	//
	// This is useful for receivers which need to perform lengthy operations at
	// certain times, such as writing data to floppy disk. If the receiver did
	// not issue a WAIT, then the transmitter might count down its 20
	// millisecond timeout, and assume a non-handshaking action such as sending
	// the next packet, without waiting for a response from the receiver. A WAIT
	// tells the transmitter to wait indefinitely for a response.
	Wait = ControlPacketType(0x7C)
)

// Message represents any SDS protocol message.
type Message interface {
	// Encode appends the encoding of the message to 'buf'.
	Encode(buf []byte) []byte
}

func (msg *DumpHeader) Encode(b []byte) []byte {
	b = append(b, 0xF0, 0x7E, msg.Channel&0x7F, 0x01)
	b = append14bit(b, msg.Number)
	b = append(b, msg.BitDepth&0x7F)
	b = append20bit(b, msg.Period)
	b = append20bit(b, msg.Length)
	b = append20bit(b, msg.LoopStart)
	b = append20bit(b, msg.LoopEnd)
	b = append(b, msg.LoopType&0x7F)
	return append(b, 0xF7)
}

func (msg *DataPacket) Encode(b []byte) []byte {
	b = append(b, 0xF0, 0x7E, msg.Channel&0x7F, 0x02)
	b = append(b, msg.PacketNumber&0x7F)
	b = append(b, msg.Data[:]...)
	b = append(b, msg.Checksum&0x7F)
	return append(b, 0xF7)
}

func (msg *DumpRequest) Encode(b []byte) []byte {
	b = append(b, 0xF0, 0x7E, msg.Channel&0x7F, 0x03)
	b = append14bit(b, msg.Number)
	return append(b, 0xF7)
}

func (msg *ControlPacket) Encode(b []byte) []byte {
	return append(b, 0xF0, 0x7E, msg.Channel&0x7F, byte(msg.Type)&0x7F, msg.PacketNumber&0x7F, 0xF7)
}

func append14bit(b []byte, num uint16) []byte {
	return append(b, byte(num)&0x7F, byte(num>>7)&0x7F)
}

func append20bit(b []byte, num uint) []byte {
	return append(b, byte(num)&0x7F, byte(num>>7)&0x7F, byte(num>>14)&0x3F)
}

const (
	dumpHeaderSize    = 21
	dumpRequestSize   = 7
	dataPacketSize    = 127
	controlPacketSize = 6
)

var (
	errNotSysex = errors.New("not a sysex message")
	errTooShort = errors.New("message too short")
	errChecksum = errors.New("bad checksum")
)

var prefix = []byte{0xF0, 0x7E}

// Decode decodes a MIDI SDS message. The buffer must contain a complete MIDI message.
func Decode(sysex []byte) (Message, error) {
	if !bytes.HasPrefix(sysex, prefix) || sysex[len(sysex)-1] != 0xF7 {
		return nil, errNotSysex
	}
	if len(sysex) < 4 {
		return nil, errTooShort
	}
	switch sysex[3] {
	case 0x01:
		return decodeDumpHeader(sysex)
	case 0x02:
		return decodeDataPacket(sysex)
	case 0x03:
		return decodeDumpRequest(sysex)
	case 0x7C, 0x7D, 0x7E, 0x7F:
		return decodeControlPacket(sysex)
	default:
		return nil, fmt.Errorf("invalid message id %x", sysex[3])
	}
}

func decodeDumpHeader(msg []byte) (Message, error) {
	if len(msg) != dumpHeaderSize {
		return nil, fmt.Errorf("bad size %d for DumpHeader", len(msg))
	}
	dec := &DumpHeader{
		Channel:   msg[2],
		Number:    dec14bit(msg[4], msg[5]),
		BitDepth:  msg[6],
		Period:    dec20bit(msg[7], msg[8], msg[9]),
		Length:    dec20bit(msg[10], msg[11], msg[12]),
		LoopStart: dec20bit(msg[13], msg[14], msg[15]),
		LoopEnd:   dec20bit(msg[16], msg[17], msg[18]),
		LoopType:  msg[19],
	}
	if dec.BitDepth < 8 || dec.BitDepth > 28 {
		return nil, fmt.Errorf("unsupported bit depth %d in DumpHeader", dec.BitDepth)
	}
	return dec, nil
}

func decodeDataPacket(msg []byte) (Message, error) {
	if len(msg) != dataPacketSize {
		return nil, fmt.Errorf("bad size %d for DataPacket", len(msg))
	}
	dec := &DataPacket{
		Channel:      msg[2],
		PacketNumber: msg[4],
		Checksum:     msg[125],
	}
	copy(dec.Data[:], msg[5:125])
	return dec, nil
}

func decodeDumpRequest(msg []byte) (Message, error) {
	if len(msg) != dumpRequestSize {
		return nil, fmt.Errorf("bad size %d for DumpRequest", len(msg))
	}
	dec := &DumpRequest{
		Channel: msg[2],
		Number:  dec14bit(msg[4], msg[5]),
	}
	return dec, nil
}

func decodeControlPacket(msg []byte) (Message, error) {
	if len(msg) != controlPacketSize {
		return nil, fmt.Errorf("bad size %d for ControlPacket", len(msg))
	}
	dec := &ControlPacket{
		Channel:      msg[2],
		Type:         ControlPacketType(msg[3]),
		PacketNumber: msg[4],
	}
	return dec, nil
}

func dec14bit(l, h byte) uint16 {
	return uint16(l&0x7F) | uint16(h&0x7F)<<7
}

func dec20bit(l, m, h byte) uint {
	return uint(l&0x7F) | uint(m&0x7F)<<7 | uint(h&0x3F)<<14
}

// ComputeChecksum returns the computed checksum of the packet.
func (msg *DataPacket) ComputeChecksum() byte {
	var buf [dataPacketSize]byte
	msg.Encode(buf[:0])
	c := buf[0]
	for i := range buf[1 : dataPacketSize-1] {
		c ^= buf[i]
	}
	return c & 0x7F
}

// GetSamples decodes the sample data in packet and appends it to s.
func (msg *DataPacket) GetSamples(s []int, bitDepth int) []int {
	switch {
	case bitDepth < 8:
		panic("bit depth < 8 is not supported")
	case bitDepth <= 14:
		return msg.read2(s, bitDepth)
	case bitDepth <= 21:
		return msg.read3(s, bitDepth)
	case bitDepth <= 28:
		return msg.read4(s, bitDepth)
	default:
		panic("bit depth > 28 is not supported")
	}
}

func (msg *DataPacket) read2(out []int, bits int) []int {
	var (
		shiftH   = bits - 7
		shiftL   = 14 - bits
		zero     = uint(1) << (bits - 1)
		buf      [len(msg.Data) / 2]int
		bufIndex int
	)
	for i := 0; i < len(msg.Data); i += 2 {
		v := uint(msg.Data[i]&0x7F) << shiftH
		v |= uint(msg.Data[i+1]&0x7F) >> shiftL
		buf[bufIndex] = int(v - zero)
		bufIndex++
	}
	return append(out, buf[:]...)
}

func (msg *DataPacket) read3(out []int, bits int) []int {
	var (
		shiftH   = bits - 7
		shiftM   = bits - 14
		shiftL   = 21 - bits
		zero     = uint(1) << (bits - 1)
		buf      [len(msg.Data) / 3]int
		bufIndex int
	)
	for i := 0; i < len(msg.Data); i += 3 {
		v := uint(msg.Data[i]&0x7F) << shiftH
		v |= uint(msg.Data[i+1]&0x7F) << shiftM
		v |= uint(msg.Data[i+2]&0x7F) >> shiftL
		buf[bufIndex] = int(v - zero)
		bufIndex++
	}
	return append(out, buf[:]...)
}

func (msg *DataPacket) read4(out []int, bits int) []int {
	var (
		shiftH   = bits - 7
		shiftM1  = bits - 14
		shiftM2  = bits - 21
		shiftL   = 28 - bits
		zero     = uint(1) << (bits - 1)
		buf      [len(msg.Data) / 4]int
		bufIndex int
	)
	for i := 0; i < len(msg.Data); i += 4 {
		v := uint(msg.Data[i]&0x7F) << shiftH
		v |= uint(msg.Data[i+1]&0x7F) << shiftM1
		v |= uint(msg.Data[i+2]&0x7F) << shiftM2
		v |= uint(msg.Data[i+3]&0x7F) >> shiftL
		buf[bufIndex] = int(v - zero)
		bufIndex++
	}
	return append(out, buf[:]...)
}

// SetSamples copies sample data into the packet. It returns the remaining samples.
func (msg *DataPacket) SetSamples(samples []int, bitDepth int) []int {
	switch {
	case bitDepth < 8:
		panic("bit depth < 8 is not supported")
	case bitDepth <= 14:
		return msg.write2(samples, bitDepth)
	case bitDepth <= 21:
		return msg.write3(samples, bitDepth)
	case bitDepth <= 28:
		return msg.write4(samples, bitDepth)
	default:
		panic("bit depth > 28 is not supported")
	}
}

func (msg *DataPacket) write2(samples []int, bits int) []int {
	var (
		shiftH = bits - 7
		shiftL = 14 - bits
		zero   = uint(1) << (bits - 1)
		si, di = 0, 0
	)
	// Encode sample data.
	for ; si < len(samples) && di < len(msg.Data); si, di = si+1, di+2 {
		s := uint(samples[si]) + zero
		msg.Data[di] = byte(s>>shiftH) & 0x7F
		msg.Data[di+1] = byte(s<<shiftL) & 0x7F
	}
	// Zero remainder of msg.Data.
	for ; di < len(msg.Data); di++ {
		msg.Data[di] = 0
	}
	return samples[si:]
}

func (msg *DataPacket) write3(samples []int, bits int) []int {
	var (
		shiftH = bits - 7
		shiftM = bits - 14
		shiftL = 21 - bits
		zero   = uint(1) << (bits - 1)
		si, di = 0, 0
	)
	// Encode sample data.
	for ; si < len(samples) && di < len(msg.Data); si, di = si+1, di+3 {
		s := uint(samples[si]) + zero
		msg.Data[di] = byte(s>>shiftH) & 0x7F
		msg.Data[di+1] = byte(s>>shiftM) & 0x7F
		msg.Data[di+2] = byte(s<<shiftL) & 0x7F
	}
	// Zero remainder of msg.Data.
	for ; di < len(msg.Data); di++ {
		msg.Data[di] = 0
	}
	return samples[si:]
}

func (msg *DataPacket) write4(samples []int, bits int) []int {
	var (
		shiftH  = bits - 7
		shiftM1 = bits - 14
		shiftM2 = bits - 21
		shiftL  = 28 - bits
		zero    = uint(1) << (bits - 1)
		si, di  = 0, 0
	)
	// Encode sample data.
	for ; si < len(samples) && di < len(msg.Data); si, di = si+1, di+4 {
		s := uint(samples[si]) + zero
		msg.Data[di] = byte(s>>shiftH) & 0x7F
		msg.Data[di+1] = byte(s>>shiftM1) & 0x7F
		msg.Data[di+2] = byte(s>>shiftM2) & 0x7F
		msg.Data[di+3] = byte(s<<shiftL) & 0x7F
	}
	// Zero remainder of msg.Data.
	for ; di < len(msg.Data); di++ {
		msg.Data[di] = 0
	}
	return samples[si:]
}
