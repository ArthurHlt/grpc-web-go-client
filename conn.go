package grpcweb

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/encoding"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/proto"
	"io"
	"net/http"
	"strings"
)

type payloadFormat uint8

const (
	payloadLen                    = 1
	sizeLen                       = 4
	headerLen                     = payloadLen + sizeLen
	compressionNone payloadFormat = 0 // no compression
	compressionMade payloadFormat = 1 // compressed
)

type Http1ClientConn struct {
	httpClient        *http.Client
	target            string
	scheme            string
	perRpcCredentials credentials.PerRPCCredentials
}

type Http1ClientConnOption func(*Http1ClientConn)

func WithPerRPCCredentials(perRpcCredentials credentials.PerRPCCredentials) Http1ClientConnOption {
	return func(h *Http1ClientConn) {
		h.perRpcCredentials = perRpcCredentials
	}
}

func WithScheme(scheme string) Http1ClientConnOption {
	return func(h *Http1ClientConn) {
		h.scheme = scheme
	}
}

func NewHttp1ClientConn(target string, httpClient *http.Client, opts ...Http1ClientConnOption) *Http1ClientConn {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	cc := &Http1ClientConn{
		httpClient: httpClient,
		target:     target,
		scheme:     "https",
	}
	for _, opt := range opts {
		opt(cc)
	}
	return cc
}

func (h *Http1ClientConn) Invoke(ctx context.Context, method string, args interface{}, reply interface{}, opts ...grpc.CallOption) error {
	u := fmt.Sprintf("%s://%s%s", h.scheme, h.target, method)
	hdrs := headersFromCtx(ctx, u, h.perRpcCredentials)

	req, err := messageToRequest(ctx, u, args, hdrs)
	if err != nil {
		return err
	}
	resp, err := h.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	codecName := "proto"
	if strings.HasPrefix(resp.Header.Get("Content-Type"), "application/grpc-web+") {
		codecName = strings.TrimPrefix(resp.Header.Get("Content-Type"), "application/grpc-web+")
	}
	codec := encoding.GetCodec(codecName)

	_, err = bodyToMessage(resp.Body, codec, reply, false)
	return err
}

func (h *Http1ClientConn) Target() string {
	return h.target
}

func (h *Http1ClientConn) NewStream(ctx context.Context, desc *grpc.StreamDesc, method string, opts ...grpc.CallOption) (grpc.ClientStream, error) {
	u := fmt.Sprintf("%s://%s%s", h.scheme, h.target, method)
	hdrs := headersFromCtx(ctx, u, h.perRpcCredentials)
	// new client for never timeout
	httpClient := &http.Client{
		Transport:     h.httpClient.Transport,
		CheckRedirect: h.httpClient.CheckRedirect,
		Jar:           h.httpClient.Jar,
		Timeout:       0,
	}
	return NewHttp1ClientStream(ctx, u, httpClient, hdrs), nil
}

func msgHeader(data, compData []byte) (hdr []byte, payload []byte) {
	hdr = make([]byte, headerLen)
	if compData != nil {
		hdr[0] = byte(compressionMade)
		data = compData
	} else {
		hdr[0] = byte(compressionNone)
	}

	// Write length of payload into buf
	binary.BigEndian.PutUint32(hdr[payloadLen:], uint32(len(data)))
	return hdr, data
}

func messageToRequest(ctx context.Context, url string, args interface{}, hdrs http.Header) (*http.Request, error) {
	argsMess, ok := args.(proto.Message)
	if !ok {
		return nil, fmt.Errorf("args is not a proto.Message")
	}
	b, err := proto.Marshal(argsMess)
	if err != nil {
		return nil, err
	}
	buf := &bytes.Buffer{}
	hdr, _ := msgHeader(b, nil)
	buf.Write(hdr)
	buf.Write(b)
	buf = bytes.NewBufferString(base64.StdEncoding.EncodeToString(buf.Bytes()))

	req, err := http.NewRequestWithContext(ctx, "POST", url, buf)
	if err != nil {
		return nil, err
	}
	for k, v := range hdrs {
		req.Header[k] = v
	}
	req.Header.Set("Content-Type", "application/grpc-web-text")
	req.Header.Set("Accept", "application/grpc-web")

	return req, nil
}

func headersFromCtx(ctx context.Context, url string, perRpcCredentials credentials.PerRPCCredentials) http.Header {
	hdrs := http.Header{}
	if mdCtx, ok := metadata.FromIncomingContext(ctx); ok {
		for k, v := range mdCtx {
			hdrs[k] = v
		}
	}

	if perRpcCredentials != nil {
		mdCreds, err := perRpcCredentials.GetRequestMetadata(ctx, url)
		if err != nil {
			return hdrs
		}
		for k, v := range mdCreds {
			hdrs.Set(k, v)
		}
	}
	return hdrs
}

func bodyToMessage(rawBody io.Reader, codec encoding.Codec, reply interface{}, noTrailer bool) (trailer metadata.MD, err error) {
	resHeader, err := ParseResponseHeader(rawBody)
	if err != nil {
		if err == io.EOF {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to parse response header: %w", err)
	}
	if resHeader.IsMessageHeader() {
		resBody, err := ParseLengthPrefixedMessage(rawBody, resHeader.ContentLength)
		if err != nil {
			return nil, fmt.Errorf("failed to parse the response body: %w", err)
		}
		if err := codec.Unmarshal(resBody, reply); err != nil {
			return nil, fmt.Errorf("failed to unmarshal response body by codec %s: %w", codec.Name(), err)
		}
		if !noTrailer {
			resHeader, err = ParseResponseHeader(rawBody)
			if err != nil {
				return nil, fmt.Errorf("failed to parse response header: %w", err)
			}
		}

	}
	if !resHeader.IsTrailerHeader() {
		if noTrailer {
			return nil, nil
		}
		return nil, fmt.Errorf("unexpected header")
	}

	statusResp, trailer, err := ParseStatusAndTrailer(rawBody, resHeader.ContentLength)
	if err != nil {
		return nil, fmt.Errorf("failed to parse status and trailer: %w", err)
	}
	return trailer, statusResp.Err()
}
