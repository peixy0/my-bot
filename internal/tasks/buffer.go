package tasks

import "fmt"

type tailBuffer struct {
	buf       []byte
	capacity  int
	truncated bool
}

func newTailBuffer(capacity int) tailBuffer {
	return tailBuffer{capacity: capacity}
}

func (b *tailBuffer) Append(text string) {
	if text == "" || b.capacity <= 0 {
		return
	}
	b.buf = append(b.buf, text...)
	if len(b.buf) <= b.capacity {
		return
	}
	b.buf = b.buf[len(b.buf)-b.capacity:]
	b.truncated = true
}

func (b tailBuffer) String(label string) string {
	if len(b.buf) == 0 {
		return ""
	}
	if !b.truncated {
		return string(b.buf)
	}
	return fmt.Sprintf("[output truncated; showing the last %d bytes of %s]\n\n%s", len(b.buf), label, string(b.buf))
}
