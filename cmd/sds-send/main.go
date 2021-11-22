package main

import (
	"flag"
	"log"
	"os"
	"time"

	"github.com/fjl/sds/internal/cmdutil"
	"github.com/fjl/sds/sds"
	"github.com/go-audio/audio"
	"github.com/go-audio/transforms"
	"github.com/go-audio/wav"
)

func main() {
	// Argument processing.
	var (
		inDevice  = flag.String("dev", "", "MIDI input device")
		outDevice = flag.String("odev", "", "MIDI output device (default: same as input)")
		channel   = flag.Int("ch", 0, "Sysex channel number")
		slot      = flag.Int("slot", 0, "Waveform slot number")
	)
	flag.Parse()
	midiConfig := cmdutil.Config{InDevice: *inDevice, OutDevice: *outDevice}
	sendConfig := sendConfig{Channel: *channel, WaveformNumber: *slot}
	if flag.NArg() != 1 {
		log.Fatal("need wave file as argument")
	}
	filename := flag.Arg(0)

	// Load .wav file.
	buffer, err := readWAV(filename)
	if err != nil {
		log.Fatal(err)
	}

	if buffer.Format.NumChannels > 1 {
		log.Println("converting to mono")
		buffer = mixToMono(buffer)
	}

	// Send the waveform data.
	conn, err := cmdutil.Open(&midiConfig)
	if err != nil {
		log.Fatal(err)
	}
	defer conn.Close()
	doTransfer(&sendConfig, conn, buffer)
}

func readWAV(file string) (*audio.IntBuffer, error) {
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
	return decoder.FullPCMBuffer()
}

func mixToMono(inputBuffer *audio.IntBuffer) *audio.IntBuffer {
	bitDepth := inputBuffer.SourceBitDepth
	fb := inputBuffer.AsFloatBuffer()
	transforms.MonoDownmix(fb)
	mono := fb.AsIntBuffer()
	mono.SourceBitDepth = bitDepth
	return mono
}

type sendConfig struct {
	Channel        int
	WaveformNumber int
}

const (
	handshakeTimeout    = 2 * time.Second
	dataResponseTimeout = 20 * time.Millisecond
)

// doTransfer sends the given waveform via SDS.
func doTransfer(cfg *sendConfig, conn *cmdutil.Conn, waveform *audio.IntBuffer) {
	header := &sds.DumpHeader{
		Channel:  byte(cfg.Channel),
		Number:   uint16(cfg.WaveformNumber),
		BitDepth: byte(waveform.SourceBitDepth),
		Period:   samplerateToPeriod(waveform.Format.SampleRate),
	}
	transfer := sds.NewSendOp(waveform.Data, header)

	// Begin transfer by sending header.
	log.Println("requesting transfer")
	send(conn, header)

	waiting := false
	for {
		switch msg := receive(conn, handshakeTimeout).(type) {
		case nil:
			if !waiting {
				log.Println("receiver did not respond, assumed to be non-handshaking")
				transferData(cfg, conn, transfer)
				return
			}
		case *sds.ControlPacket:
			if msg.Channel != byte(cfg.Channel) {
				continue
			}
			waiting = false
			switch msg.Type {
			case sds.Ack:
				if msg.PacketNumber != 0 {
					continue
				}
				log.Println("<< ACK")
				transferData(cfg, conn, transfer)
				return
			case sds.Nak:
				log.Fatal("transfer denied: NAK response")
			case sds.Cancel:
				log.Fatal("transfer denied: CANCEL response")
			case sds.Wait:
				log.Println("<< WAIT")
				waiting = true
				continue
			}
		default:
			log.Printf("ignoring message %#v", msg)
		}
	}
}

func transferData(cfg *sendConfig, conn *cmdutil.Conn, transfer *sds.SendOp) {
	var (
		progress int
		waiting  bool
	)
	for !transfer.Done() {
		if !waiting {
			send(conn, transfer.NextMessage())
		}
		switch msg := receive(conn, dataResponseTimeout).(type) {
		case nil:
			// ignore
		case *sds.ControlPacket:
			if msg.Channel != byte(cfg.Channel) {
				continue
			}
			waiting = false
			switch msg.Type {
			case sds.Ack:
				// Packet confirmed.
			case sds.Nak:
				log.Fatalf("<< NAK (packet %d)", msg.PacketNumber)
			case sds.Cancel:
				log.Fatalf("<< CANCEL")
			case sds.Wait:
				log.Println("<< WAIT")
				waiting = true
			}
		}

		p := transfer.Progress()
		if p-progress > 5 || (p == 100 && progress != 100) {
			progress = p
			log.Printf("progress: %d%%", progress)
		}
	}
}

func send(conn *cmdutil.Conn, msg sds.Message) {
	_, err := conn.Write(msg.Encode(nil))
	if err != nil {
		log.Fatal(err)
	}
}

func receive(conn *cmdutil.Conn, timeout time.Duration) sds.Message {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	for {
		select {
		case rawmsg := <-conn.PacketCh:
			msg, err := sds.Decode(rawmsg)
			if err != nil {
				log.Printf("msg %x: %v", rawmsg, err)
				continue
			}
			return msg
		case <-timer.C:
			return nil
		}
	}
}

func wait(conn *cmdutil.Conn) sds.Message {
	for {
		msg := receive(conn, 2*time.Second)
		switch msg := msg.(type) {
		case *sds.ControlPacket:
			if msg.Type != sds.Wait {
				return msg
			}
		}
	}
}

func samplerateToPeriod(rate int) uint {
	return uint(1000000000 / rate)
}
