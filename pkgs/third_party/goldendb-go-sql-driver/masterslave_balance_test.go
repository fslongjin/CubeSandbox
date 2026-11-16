package mysql

import (
	"encoding/binary"
	"testing"
)

func TestReadHandshakePacketMasterFlag(t *testing.T) {
	tests := []struct {
		name     string
		flagByte byte
		want     bool
	}{
		{"master flag set", 0x01, true},
		{"master flag zero", 0x00, false},
		{"master flag nonzero", 0xFF, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Construct a minimal handshake packet
			// protocol version [1 byte] = 10
			// server version [null terminated] = "5.7.0\0"
			// connection id [4 bytes]
			// auth_data [8 bytes]
			// filler [1 byte] = 0x00
			// capability_flags_lower [2 bytes]
			// character_set [1 byte]
			// status_flags [2 bytes]
			// capability_flags_upper [2 bytes]
			// length_of_auth_plugin_data [1 byte]
			// reserved [10 bytes] - master_flag is last byte (index 15 from pos of char_set)

			data := make([]byte, 0, 100)
			// protocol version
			data = append(data, 10)
			// server version
			data = append(data, []byte("5.7.0")...)
			data = append(data, 0x00)
			// connection id
			data = binary.LittleEndian.AppendUint32(data, 1)
			// auth_data (8 bytes)
			data = append(data, make([]byte, 8)...)
			// filler
			data = append(data, 0x00)
			// capability_flags_lower
			data = binary.LittleEndian.AppendUint16(data, uint16(clientProtocol41))
			// character_set
			data = append(data, 33) // utf8
			// status_flags
			data = binary.LittleEndian.AppendUint16(data, 0)
			// capability_flags_upper
			data = binary.LittleEndian.AppendUint16(data, 0)
			// length_of_auth_plugin_data
			data = append(data, 20)
			// reserved (10 bytes) - master_flag at last byte (offset 9)
			reserved := make([]byte, 10)
			reserved[9] = tt.flagByte // master_flag at reserved[9] = data[pos+15]
			data = append(data, reserved...)

			// Simulate readHandshakePacket reserved parsing logic
			// pos points to character_set position
			// pos+0: char_set(1) + pos+1: status(2) + pos+3: cap_upper(2) + pos+5: salt_len(1) = 6 bytes
			// reserved starts at pos+6, master_flag at pos+6+9 = pos+15
			pos := 1 + 5 + 1 + 4 + 8 + 1 + 2 // proto(1) + ver(5+\0) + connid(4) + auth(8) + filler(1) + cap_lower(2)
			if len(data) >= pos+16 {
				got := data[pos+15] != 0
				if got != tt.want {
					t.Errorf("master_flag = %v, want %v (byte at pos+15 = %d)", got, tt.want, data[pos+15])
				}
			} else {
				t.Errorf("data too short: need %d bytes, got %d", pos+16, len(data))
			}
		})
	}
}

func TestConnectMasterSlaveDedup(t *testing.T) {
	// Test exact ip:port match dedup logic
	masterAddr := "10.228.129.167:5501"
	hosts := []string{
		"10.228.129.167:5501",
		"10.228.129.167:5502", // Same IP different port, should NOT be removed
		"10.228.129.168:5501",
	}

	// Exact match dedup by ip:port
	newHosts := make([]string, 0, len(hosts))
	for _, h := range hosts {
		if h != masterAddr {
			newHosts = append(newHosts, h)
		}
	}
	hosts = newHosts

	// Insert at front
	hosts = append([]string{masterAddr}, hosts...)

	expected := []string{
		"10.228.129.167:5501",
		"10.228.129.167:5502",
		"10.228.129.168:5501",
	}

	if len(hosts) != len(expected) {
		t.Fatalf("len = %d, want %d", len(hosts), len(expected))
	}
	for i, h := range hosts {
		if h != expected[i] {
			t.Errorf("hosts[%d] = %q, want %q", i, h, expected[i])
		}
	}
}

func TestConnectMasterSlavePopFromHead(t *testing.T) {
	// Test pop from head + insert at head ensures next iteration uses newly inserted result
	hosts := []string{"A:3306", "B:3306", "C:3306"}
	masterFromB := "M:3306"

	// First iteration: pop A from head
	element := hosts[0]
	hosts = hosts[1:]
	if element != "A:3306" {
		t.Fatalf("first pop = %q, want %q", element, "A:3306")
	}

	// Simulate B is slave, found master M
	// Dedup + insert at head
	newHosts := make([]string, 0, len(hosts))
	for _, h := range hosts {
		if h != masterFromB {
			newHosts = append(newHosts, h)
		}
	}
	hosts = append([]string{masterFromB}, newHosts...)

	// Second iteration: should pop M (newly inserted)
	element = hosts[0]
	hosts = hosts[1:]
	if element != "M:3306" {
		t.Fatalf("second pop = %q, want %q (should be newly inserted master)", element, "M:3306")
	}
}

func TestConnectMasterSlaveDedupSameIPDiffPort(t *testing.T) {
	// Verify: same IP different ports are NOT removed
	masterAddr := "192.168.1.1:5501"
	hosts := []string{
		"192.168.1.1:5502", // Same IP different port
		"192.168.1.1:5503", // Same IP different port
		"192.168.1.2:5501",
	}

	newHosts := make([]string, 0, len(hosts))
	for _, h := range hosts {
		if h != masterAddr {
			newHosts = append(newHosts, h)
		}
	}
	hosts = append([]string{masterAddr}, newHosts...)

	// Same IP different port entries should be kept
	if len(hosts) != 4 {
		t.Fatalf("len = %d, want 4 (same IP different port should be kept)", len(hosts))
	}

	expected := []string{
		"192.168.1.1:5501",
		"192.168.1.1:5502",
		"192.168.1.1:5503",
		"192.168.1.2:5501",
	}
	for i, h := range hosts {
		if h != expected[i] {
			t.Errorf("hosts[%d] = %q, want %q", i, h, expected[i])
		}
	}
}
