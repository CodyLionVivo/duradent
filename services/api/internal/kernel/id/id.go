// Package id provides the identifier type shared by every bounded context:
// a UUIDv7, stored as BINARY(16). Version 7 is time-ordered, so new rows land
// at the end of the InnoDB clustered index instead of splitting random pages.
package id

import (
	"crypto/rand"
	"database/sql/driver"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"time"
)

type ID [16]byte

var Nil ID

var ErrInvalid = errors.New("invalid id")

var (
	mu       sync.Mutex
	lastMS   int64
	sequence uint16
)

// New returns a UUIDv7 that is monotonic within this process, even for ids
// created in the same millisecond.
func New() ID {
	mu.Lock()
	ms := time.Now().UnixMilli()
	if ms <= lastMS {
		ms = lastMS
		sequence++
		if sequence > 0x0fff { // 12-bit counter exhausted: borrow the next millisecond
			ms++
			sequence = 0
		}
	} else {
		sequence = 0
	}
	lastMS = ms
	seq := sequence
	mu.Unlock()

	var u ID
	var ts [8]byte
	binary.BigEndian.PutUint64(ts[:], uint64(ms)) //nolint:gosec // unix ms is positive
	copy(u[0:6], ts[2:8])
	_, _ = rand.Read(u[6:])
	u[6] = 0x70 | byte(seq>>8)&0x0f // version 7 + high bits of the counter
	u[7] = byte(seq & 0xff)         //nolint:gosec // masked to 8 bits
	u[8] = 0x80 | u[8]&0x3f         // RFC 9562 variant
	return u
}

func Parse(s string) (ID, error) {
	var u ID
	if len(s) != 36 || s[8] != '-' || s[13] != '-' || s[18] != '-' || s[23] != '-' {
		return Nil, fmt.Errorf("%w: %q", ErrInvalid, s)
	}
	clean := s[0:8] + s[9:13] + s[14:18] + s[19:23] + s[24:36]
	if _, err := hex.Decode(u[:], []byte(clean)); err != nil {
		return Nil, fmt.Errorf("%w: %q", ErrInvalid, s)
	}
	return u, nil
}

func MustParse(s string) ID {
	u, err := Parse(s)
	if err != nil {
		panic(err)
	}
	return u
}

func (u ID) IsNil() bool { return u == Nil }

func (u ID) String() string {
	var b [36]byte
	hex.Encode(b[0:8], u[0:4])
	b[8] = '-'
	hex.Encode(b[9:13], u[4:6])
	b[13] = '-'
	hex.Encode(b[14:18], u[6:8])
	b[18] = '-'
	hex.Encode(b[19:23], u[8:10])
	b[23] = '-'
	hex.Encode(b[24:], u[10:])
	return string(b[:])
}

func (u ID) MarshalText() ([]byte, error) { return []byte(u.String()), nil }

func (u *ID) UnmarshalText(b []byte) error {
	p, err := Parse(string(b))
	if err != nil {
		return err
	}
	*u = p
	return nil
}

// Value stores the id as BINARY(16).
func (u ID) Value() (driver.Value, error) { return u[:], nil }

func (u *ID) Scan(src any) error {
	b, ok := src.([]byte)
	if !ok || len(b) != 16 {
		return fmt.Errorf("%w: cannot scan %T", ErrInvalid, src)
	}
	copy(u[:], b)
	return nil
}
