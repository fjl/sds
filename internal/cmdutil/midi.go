package cmdutil

import (
	"fmt"
	"log"
	"strings"

	"gitlab.com/gomidi/midi"
	driver "gitlab.com/gomidi/rtmididrv"
)

type Config struct {
	OutDevice string
	InDevice  string
}

type Conn struct {
	PacketCh chan []byte   // receives all sysex messages
	CloseCh  chan struct{} // closed by Close

	in  midi.In
	out midi.Out
}

// Open opens the MIDI connection.
func Open(cfg *Config) (*Conn, error) {
	in, out, err := findDevices(cfg)
	if err != nil {
		return nil, err
	}
	log.Println("midi input:", in)
	log.Println("midi output:", out)
	if err := in.Open(); err != nil {
		return nil, fmt.Errorf("can't open MIDI input: %v", err)
	}
	if err := out.Open(); err != nil {
		in.Close()
		return nil, fmt.Errorf("can't open MIDI output: %v", err)
	}

	var packetCh = make(chan []byte, 512)
	in.SetListener(func(msg []byte, deltaT int64) {
		if !isSysex(msg) {
			return
		}
		select {
		case packetCh <- msg:
		default:
		}
	})

	c := &Conn{PacketCh: packetCh, CloseCh: make(chan struct{}), in: in, out: out}
	return c, nil
}

func isSysex(msg []byte) bool {
	return len(msg) > 0 && msg[0] == 0xf0 && msg[len(msg)-1] == 0xf7
}

func (c *Conn) Write(msg []byte) (int, error) {
	return c.out.Write(msg)
}

func (c *Conn) Close() {
	close(c.CloseCh)
	c.in.Close()
	c.out.Close()
}

func findDevices(cfg *Config) (midi.In, midi.Out, error) {
	drv, err := driver.New(driver.IgnoreActiveSense(), driver.IgnoreTimeCode())
	if err != nil {
		return nil, nil, err
	}
	inputs, err := drv.Ins()
	if err != nil {
		return nil, nil, fmt.Errorf("can't list MIDI inputs: %v", err)
	}
	outputs, err := drv.Outs()
	if err != nil {
		return nil, nil, fmt.Errorf("can't list MIDI outputs: %v", err)
	}
	if len(inputs) == 0 {
		return nil, nil, fmt.Errorf("no MIDI inputs")
	}

	// Find a matching input device.
	var selectedIn midi.In
	if cfg.InDevice == "" {
		selectedIn = inputs[0]
	} else {
		var inputNames []string
		for _, in := range inputs {
			name := in.String()
			inputNames = append(inputNames, name)
			if strings.Contains(strings.ToLower(name), strings.ToLower(cfg.InDevice)) {
				selectedIn = in
				break
			}
		}
		if selectedIn == nil {
			return nil, nil, fmt.Errorf("can't find MIDI input device %q, have %v", cfg.InDevice, inputNames)
		}
	}

	// Find the output device.
	outDevice := cfg.OutDevice
	if outDevice == "" {
		outDevice = selectedIn.String()
	}
	var selectedOut midi.Out
	var outputNames []string
	for _, out := range outputs {
		outputNames = append(outputNames, out.String())
		if out.String() == outDevice {
			selectedOut = out
			break
		}
	}
	if selectedOut == nil {
		return nil, nil, fmt.Errorf("can't find MIDI output device %q, have %v", outDevice, outputNames)
	}
	return selectedIn, selectedOut, nil
}
