// this parser has been taken from https://github.com/ktr0731/grpc-web-go-client/tree/master/grpcweb/parser
// the license is:
//MIT License
//
//Copyright (c) 2018 ktr0731
//
//Permission is hereby granted, free of charge, to any person obtaining a copy
//of this software and associated documentation files (the "Software"), to deal
//in the Software without restriction, including without limitation the rights
//to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
//copies of the Software, and to permit persons to whom the Software is
//furnished to do so, subject to the following conditions:
//
//The above copyright notice and this permission notice shall be included in all
//copies or substantial portions of the Software.
//
//THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
//IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
//FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
//AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
//LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
//OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
// SOFTWARE

package grpcweb

import (
	"bufio"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"io"
	"strconv"
	"strings"

	spb "google.golang.org/genproto/googleapis/rpc/status"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

type Header struct {
	flag          byte
	ContentLength uint32
}

func (h *Header) IsMessageHeader() bool {
	return h.flag == 0 || h.flag == 1
}

func (h *Header) IsTrailerHeader() bool {
	return h.flag>>7 == 0x01
}

func ParseResponseHeader(r io.Reader) (*Header, error) {
	var h [5]byte
	n, err := r.Read(h[:])
	if err != nil {
		return nil, fmt.Errorf("failed to read header: %w", err)
	}
	if n != len(h) {
		return nil, io.ErrUnexpectedEOF
	}

	length := binary.BigEndian.Uint32(h[1:])
	if length == 0 {
		return nil, io.EOF
	}
	return &Header{
		flag:          h[0],
		ContentLength: length,
	}, nil
}

func ParseLengthPrefixedMessage(r io.Reader, length uint32) ([]byte, error) {
	content := make([]byte, length)
	n, err := r.Read(content)
	switch {
	case uint32(n) != length:
		return nil, io.ErrUnexpectedEOF
	case err == io.EOF:
		return nil, io.EOF
	case err != nil:
		return nil, err
	}
	return content, nil
}

func ParseStatusAndTrailer(r io.Reader, length uint32) (*status.Status, metadata.MD, error) {
	var (
		readLen    uint32
		headerStat *status.Status
		code       codes.Code
		msg        string
	)
	trailer := metadata.New(nil)
	s := bufio.NewScanner(r)
	for s.Scan() {
		readLen += uint32(len(s.Bytes()))
		if readLen > length {
			return nil, nil, io.ErrUnexpectedEOF
		}

		t := s.Text()
		i := strings.Index(t, ":")
		if i == -1 {
			return nil, nil, io.ErrUnexpectedEOF
		}

		// Check reserved keys.
		k, v := strings.ToLower(t[:i]), strings.TrimSpace(t[i+1:])
		switch k {
		case "grpc-status":
			n, err := strconv.ParseUint(v, 10, 32)
			if err != nil {
				code = codes.Unknown
			} else {
				code = codes.Code(uint32(n))
			}
			continue
		case "grpc-message":
			msg = v
			continue
		case "grpc-status-details-bin":
			b, err := decodeBase64Value(v)
			if err != nil {
				// Same behavior as grpc/grpc-go.
				return status.Newf(
					codes.Internal,
					"transport: malformed grpc-status-details-bin: %v",
					err,
				), nil, nil
			}

			s := &spb.Status{}
			if err := proto.Unmarshal(b, s); err != nil {
				return status.Newf(
					codes.Internal,
					"transport: malformed grpc-status-details-bin: %v",
					err,
				), nil, nil
			}
			headerStat = status.FromProto(s)
		default:
			trailer.Append(k, v)
		}
	}

	var stat *status.Status
	if headerStat != nil {
		stat = headerStat
	} else {
		stat = status.New(code, msg)
	}

	if trailer.Len() == 0 {
		return stat, nil, nil
	}
	return stat, trailer, nil
}

func decodeBase64Value(v string) ([]byte, error) {
	// Mostly copied from http_util.go in grpc/grpc-go.

	if len(v)%4 == 0 {
		// Input was padded, or padding was not necessary.
		return base64.StdEncoding.DecodeString(v)
	}
	return base64.RawStdEncoding.DecodeString(v)
}
