package sourcemapx

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"go/token"
	"io"
)

// A magic byte in the generated code output that indicates a beginning of a
// source map hint. The character has been chosen because it should never show
// up in valid generated code unescaped, other than for source map hint purposes.
const HintMagic byte = '\b'

// Hint is a container for a sourcemap hint that can be embedded into the
// generated code stream. Payload size and semantics depend on the nature of the
// hint.
//
// Within the stream, the hint is encoded in the following binary format:
//   - magic: 0x08 - ASCII backspace, magic symbol indicating the beginning of the hint;
//   - size: 16 bit, big endian unsigned int - the size of the payload.
//   - payload: [size]byte - the payload of the hint.
type Hint struct {
	Payload []byte
}

// FindHint returns the lowest index in the byte slice where a source map Hint
// is embedded or -1 if it isn't found. Invariant: if FindHint(b) != -1 then
// b[FindHint(b)] == '\b'.
func FindHint(b []byte) int {
	return bytes.IndexByte(b, HintMagic)
}

// ReadHint reads the Hint from the beginning of the byte slice and returns
// the hint and the number of bytes in the slice it occupies. The caller is
// expected to find the location of the hint using FindHint prior to calling
// this function.
//
// Returned hint payload does not share backing array with b.
//
// Function panics if:
//   - b[0] != '\b'
//   - len(b) < size + 3
func ReadHint(b []byte) (h Hint, length int) {
	if len(b) < 3 {
		panic(fmt.Errorf("byte slice too short to contain hint header: len(b) = %d", len(b)))
	}
	if b[0] != HintMagic {
		panic(fmt.Errorf("byte slice doesn't start with magic 0x%x: b[0] = 0x%x", HintMagic, b[0]))
	}
	size := int(binary.BigEndian.Uint16(b[1:3]))
	if len(b) < size+3 {
		panic(fmt.Errorf("byte slice it too short to contain hint payload: len(b) = %d, expected hint size: %d", len(b), size+3))
	}

	h.Payload = make([]byte, size)
	copy(h.Payload, b[3:])
	return h, size + 3
}

// WriteTo the encoded hint into the output stream. Panics if payload is longer
// than 0xFFFF bytes.
func (h *Hint) WriteTo(w io.Writer) (int64, error) {
	if len(h.Payload) > 0xFFFF {
		panic(fmt.Errorf("hint payload may not be longer than %d bytes, got: %d", 0xFFFF, len(h.Payload)))
	}
	encoded := []byte{HintMagic}
	encoded = binary.BigEndian.AppendUint16(encoded, uint16(len(h.Payload)))
	encoded = append(encoded, h.Payload...)

	n, err := w.Write(encoded)
	if err != nil {
		return int64(n), fmt.Errorf("failed to write hint: %w", err)
	}

	return int64(n), nil
}

// Pack the given value into hint's payload.
//
// Supported types: go/token.Pos, Identifier.
//
// The first byte of the payload will indicate the encoded type, and the rest
// is an opaque, type-dependent binary representation of the type.
func (h *Hint) Pack(value any) error {
	switch v := value.(type) {
	case token.Pos:
		// type flag (1 byte) + int32 (4 bytes)
		h.Payload = binary.BigEndian.AppendUint32([]byte{1}, uint32(v))
	case Identifier:
		// type flag (1 byte) + pos (4 bytes) + name length (2 bytes) + name + origName length (2 bytes) + origName
		size := 1 + 4 + 2 + len(v.Name) + 2 + len(v.OriginalName)
		buf := make([]byte, 0, size)
		buf = append(buf, 2)
		buf = binary.BigEndian.AppendUint32(buf, uint32(v.OriginalPos))
		buf = binary.BigEndian.AppendUint16(buf, uint16(len(v.Name)))
		buf = append(buf, v.Name...)
		buf = binary.BigEndian.AppendUint16(buf, uint16(len(v.OriginalName)))
		buf = append(buf, v.OriginalName...)
		h.Payload = buf
	default:
		return fmt.Errorf("unsupported hint payload type %T", value)
	}
	return nil
}

// Unpack and return hint's payload, previously packed by Pack().
func (h *Hint) Unpack() (any, error) {
	if len(h.Payload) < 1 {
		return nil, fmt.Errorf("payload is too short to contain type flag")
	}
	switch h.Payload[0] {
	case 1:
		if len(h.Payload) < 5 {
			return nil, fmt.Errorf("payload too short for token.Pos")
		}
		return token.Pos(binary.BigEndian.Uint32(h.Payload[1:5])), nil
	case 2:
		b := h.Payload[1:]
		if len(b) < 4 {
			return nil, fmt.Errorf("payload too short for Identifier")
		}
		pos := token.Pos(binary.BigEndian.Uint32(b[:4]))
		b = b[4:]
		if len(b) < 2 {
			return nil, fmt.Errorf("payload too short for Identifier name length")
		}
		nameLen := int(binary.BigEndian.Uint16(b[:2]))
		b = b[2:]
		if len(b) < nameLen {
			return nil, fmt.Errorf("payload too short for Identifier name")
		}
		name := string(b[:nameLen])
		b = b[nameLen:]
		if len(b) < 2 {
			return nil, fmt.Errorf("payload too short for Identifier original name length")
		}
		origNameLen := int(binary.BigEndian.Uint16(b[:2]))
		b = b[2:]
		if len(b) < origNameLen {
			return nil, fmt.Errorf("payload too short for Identifier original name")
		}
		origName := string(b[:origNameLen])
		return Identifier{
			Name:         name,
			OriginalName: origName,
			OriginalPos:  pos,
		}, nil
	default:
		return nil, fmt.Errorf("unsupported hint payload type flag: %d", h.Payload[0])
	}
}
