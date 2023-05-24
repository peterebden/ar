/*
Copyright (c) 2013 Blake Smith <blakesmith0@gmail.com>

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in
all copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN
THE SOFTWARE.
*/
package ar

import (
	"bytes"
	"errors"
	"io"
	"strconv"
)

var (
	ErrWriteTooLong = errors.New("ar: write too long")
)

// Writer provides sequential writing of an ar archive.
// An ar archive is sequence of header file pairs
// Call WriteHeader to begin writing a new file, then call Write to supply the file's data
//
// Example:
// archive := ar.NewWriter(writer)
// archive.WriteGlobalHeader()
// header := new(ar.Header)
// header.Size = 15 // bytes
// if err := archive.WriteHeader(header); err != nil {
// 	return err
// }
// io.Copy(archive, data)
type Writer struct {
	w             io.Writer
	nb            int64          // number of unwritten bytes for the current file entry
	longFilenames map[string]int // content for the GNU long filenames entry (if needed)
}

// Create a new ar writer that writes to w
func NewWriter(w io.Writer) *Writer { return &Writer{w: w, longFilenames: map[string]int{}} }

func (aw *Writer) numeric(b []byte, x int64) {
	s := strconv.FormatInt(x, 10)
	for len(s) < len(b) {
		s = s + " "
	}
	copy(b, []byte(s))
}

func (aw *Writer) octal(b []byte, x int64) {
	s := "100" + strconv.FormatInt(x, 8)
	for len(s) < len(b) {
		s = s + " "
	}
	copy(b, []byte(s))
}

func (aw *Writer) string(b []byte, str string) {
	s := str
	for len(s) < len(b) {
		s = s + " "
	}
	copy(b, []byte(s))
}

// Writes to the current entry in the ar archive
// Returns ErrWriteTooLong if more than header.Size
// bytes are written after a call to WriteHeader
func (aw *Writer) Write(b []byte) (n int, err error) {
	if int64(len(b)) > aw.nb {
		b = b[0:aw.nb]
		err = ErrWriteTooLong
	}
	n, werr := aw.w.Write(b)
	aw.nb -= int64(n)
	if werr != nil {
		return n, werr
	}

	if len(b)%2 == 1 { // data size must be aligned to an even byte
		if _, err := aw.w.Write([]byte{'\n'}); err != nil {
			// Return n although we actually wrote n+1 bytes.
			// This is to make io.Copy() to work correctly.
			return n, err
		}
	}

	return
}

func (aw *Writer) WriteGlobalHeader() error {
	_, err := aw.w.Write([]byte(GLOBAL_HEADER))
	return err
}

// WriteGlobalHeaderForLongFiles writes the global header, and any GNU-style entries to handle
// "long" filenames (i.e. ones over 16 chars).
// If you do not call this (and just call WriteGlobalHeader) then long filenames will be written
// in BSD style later on.
func (aw *Writer) WriteGlobalHeaderForLongFiles(filenames []string) error {
	if err := aw.WriteGlobalHeader(); err != nil {
		return err
	}
	var data []byte
	for _, filename := range filenames {
		if len(filename) >= 16 {
			aw.longFilenames[filename] = len(data)
			data = append(data, []byte(filename)...)
			data = append(data, '/')
			data = append(data, '\n')
		}
	}
	if len(data) == 0 {
		return nil
	}
	// need at least one long filename
	if err := aw.WriteHeader(&Header{Name: "//", Mode: 0420, Size: int64(len(data))}); err != nil {
		return err
	}
	_, err := io.Copy(aw, bytes.NewReader(data))
	return err
}

// Writes the header to the underlying writer and prepares
// to receive the file payload
func (aw *Writer) WriteHeader(hdr *Header) error {
	aw.nb = int64(hdr.Size)
	header := make([]byte, HEADER_BYTE_SIZE)
	s := slicer(header)

	var bsdName []byte
	if len(hdr.Name) >= 16 {
		idx, present := aw.longFilenames[hdr.Name]
		if present {
			// already known, write GNU-style name
			aw.string(s.next(16), "/"+strconv.Itoa(idx))
		} else {
			// not known, assume they want BSD-style names.
			bsdName = append([]byte(hdr.Name), 0, 0) // seems to pad with at least two nulls
			if len(bsdName)%2 != 0 {
				bsdName = append(bsdName, 0) // pad out to an even number
			}
			aw.string(s.next(16), "#1/"+strconv.Itoa(len(bsdName)))
			// These seem to pad with two nulls?
			aw.nb += int64(len(bsdName))
			hdr.Size += int64(len(bsdName))
		}
	} else {
		aw.string(s.next(16), hdr.Name)
	}
	aw.numeric(s.next(12), hdr.ModTime.Unix())
	aw.numeric(s.next(6), int64(hdr.Uid))
	aw.numeric(s.next(6), int64(hdr.Gid))
	aw.octal(s.next(8), hdr.Mode)
	aw.numeric(s.next(10), hdr.Size)
	aw.string(s.next(2), "`\n")

	_, err := aw.w.Write(header)

	if err == nil && bsdName != nil {
		// BSD-style writes the name before the data section
		_, err = aw.Write(bsdName)
	}

	return err
}
