package main

import (
	"errors"
	"io"
)

type seekBuffer struct {
	buf []byte
	pos int
}

func (sb *seekBuffer) Reset() {
	sb.buf = sb.buf[:0]
	sb.pos = 0
}

func (sb *seekBuffer) Write(p []byte) (n int, err error) {
	n = len(p)
	if sb.pos+n > len(sb.buf) {
		if sb.pos+n > cap(sb.buf) {
			b := sb.buf
			sb.buf = make([]byte, sb.pos+n, 2*len(sb.buf)+n)
			copy(sb.buf, b)
		} else {
			sb.buf = sb.buf[:sb.pos+n]
		}
	}
	sb.pos += copy(sb.buf[sb.pos:], p)
	return n, nil
}

func (sb *seekBuffer) Seek(offset int64, whence int) (int64, error) {
	switch whence {
	case io.SeekStart:
		if offset < 0 {
			return int64(sb.pos), errors.New("invalid negative seek relative to start")
		}
		sb.pos = int(offset)
	case io.SeekEnd:
		if offset >= 0 {
			sb.pos = len(sb.buf)
		} else {
			sb.pos = len(sb.buf) + int(offset)
		}
	case io.SeekCurrent:
		sb.pos += int(offset)
	}
	if sb.pos < 0 {
		sb.pos = 0
	} else if sb.pos > len(sb.buf) {
		sb.pos = len(sb.buf)
	}
	return int64(sb.pos), nil
}
