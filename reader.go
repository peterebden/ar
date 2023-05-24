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
	"io"
	"io/ioutil"
	"os"
	"strconv"
	"strings"
	"time"
)

// Reader provides read access to an ar archive.
// Call next to skip files.
//
// N.B. This understands the GNU-style long file entries and will transparently decode them.
//
// Example:
//
//     reader := NewReader(f)
//     var buf bytes.Buffer
//     for {
//         _, err := reader.Next()
//         if err == io.EOF {
//             break
//         }
//         if err != nil {
//             t.Errorf(err.Error())
//         }
//         io.Copy(&buf, reader)
//     }
type Reader struct {
	r             io.Reader
	nb            int64
	pad           int64
	longFilenames []byte
}

// NewReader creates a new reader reading from r. It strips the global ar header.
func NewReader(r io.Reader) *Reader {
	io.CopyN(ioutil.Discard, r, 8) // Discard global header

	return &Reader{r: r}
}

func (rd *Reader) string(b []byte) string {
	i := len(b) - 1
	for i > 0 && b[i] == 32 {
		i--
	}

	return string(b[0 : i+1])
}

func (rd *Reader) numeric(b []byte) int64 {
	i := len(b) - 1
	for i > 0 && b[i] == 32 {
		i--
	}

	n, _ := strconv.ParseInt(string(b[0:i+1]), 10, 64)

	return n
}

func (rd *Reader) octal(b []byte) int64 {
	i := len(b) - 1
	for i > 0 && b[i] == 32 {
		i--
	}

	n, _ := strconv.ParseInt(string(b[0:i+1]), 8, 64)

	return n
}

func (rd *Reader) skipUnread() error {
	skip := rd.nb + rd.pad
	rd.nb, rd.pad = 0, 0
	if seeker, ok := rd.r.(io.Seeker); ok {
		_, err := seeker.Seek(skip, os.SEEK_CUR)
		return err
	}

	_, err := io.CopyN(ioutil.Discard, rd.r, skip)
	return err
}

func (rd *Reader) readHeader() (*Header, error) {
	headerBuf := make([]byte, HEADER_BYTE_SIZE)
	if _, err := io.ReadFull(rd.r, headerBuf); err != nil {
		return nil, err
	}

	header := new(Header)
	s := slicer(headerBuf)

	header.Name = rd.string(s.next(16))
	header.ModTime = time.Unix(rd.numeric(s.next(12)), 0)
	header.Uid = int(rd.numeric(s.next(6)))
	header.Gid = int(rd.numeric(s.next(6)))
	header.Mode = rd.octal(s.next(8))
	header.Size = rd.numeric(s.next(10))

	rd.nb = int64(header.Size)
	if header.Size%2 == 1 {
		rd.pad = 1
	} else {
		rd.pad = 0
	}

	// Handle long filenames
	if header.Name[0] == '/' && rd.longFilenames != nil {
		if offset, err := strconv.Atoi(header.Name[1:]); err == nil {
			data := rd.longFilenames[offset:]
			if idx := bytes.IndexByte(data, '\n'); idx != -1 {
				data = data[:idx]
			}
			if idx := bytes.IndexByte(data, '/'); idx != -1 {
				data = data[:idx]
			}
			header.Name = string(data)
		}
	}

	return header, nil
}

// Next skips to the next file in the archive file.
// Returns a Header which contains the metadata about the
// file in the archive. io.EOF is returned at the end of the input.
func (rd *Reader) Next() (*Header, error) {
	err := rd.skipUnread()
	if err != nil {
		return nil, err
	}

	hdr, err := rd.readHeader()
	if err != nil {
		return nil, err
	} else if strings.HasPrefix(hdr.Name, "#1/") {
		return hdr, rd.handleBSD(hdr)
	} else if hdr.Name != "//" {
		return hdr, err
	}
	// if we get here we have a GNU-style long file entry, read it.
	var buf bytes.Buffer
	if _, err := io.Copy(&buf, rd); err != nil {
		return nil, err
	}
	rd.longFilenames = buf.Bytes()
	return rd.Next()
}

// handleBSD handles BSD-style long file names, which are stored on the front of the data section.
func (rd *Reader) handleBSD(hdr *Header) error {
	length, err := strconv.Atoi(hdr.Name[3:])
	if err != nil {
		return err
	}
	hdr.Size -= int64(length)
	b := make([]byte, length)
	if _, err := rd.Read(b); err != nil {
		return err
	}
	// names are sometimes padded out with nulls
	hdr.Name = string(bytes.TrimRightFunc(b, func(r rune) bool { return r == 0 }))
	return nil
}

// Read reads data from the current entry in the archive.
func (rd *Reader) Read(b []byte) (n int, err error) {
	if rd.nb == 0 {
		return 0, io.EOF
	}
	if int64(len(b)) > rd.nb {
		b = b[0:rd.nb]
	}
	n, err = rd.r.Read(b)
	rd.nb -= int64(n)

	return
}
