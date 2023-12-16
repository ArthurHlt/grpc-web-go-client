package grpcweb

import (
	"context"
	"fmt"
	"google.golang.org/grpc/encoding"
	"google.golang.org/grpc/metadata"
	"net/http"
	"strings"
	"sync"
)

type Http1ClientStream struct {
	httpClient *http.Client
	url        string
	hasHeader  chan struct{}
	mutexRecv  *sync.Mutex
	headers    metadata.MD
	trailers   metadata.MD
	ctx        context.Context
	cancelFunc context.CancelFunc
	resp       *http.Response
	hdrs       http.Header
}

func NewHttp1ClientStream(ctx context.Context, url string, httpClient *http.Client, hdrs http.Header) *Http1ClientStream {
	ctxReq, cancelFunc := context.WithCancel(context.Background())
	client := &Http1ClientStream{
		httpClient: httpClient,
		url:        url,
		hasHeader:  make(chan struct{}),
		ctx:        ctxReq,
		cancelFunc: cancelFunc,
		mutexRecv:  &sync.Mutex{},
		hdrs:       hdrs,
	}
	go func() {
		<-ctx.Done()
		client.close() // nolint: errcheck
	}()
	return client
}

func (h *Http1ClientStream) Header() (metadata.MD, error) {
	<-h.hasHeader
	return h.headers, nil
}

func (h *Http1ClientStream) Trailer() metadata.MD {
	return h.trailers
}

func (h *Http1ClientStream) CloseSend() error {
	return nil
}

func (h *Http1ClientStream) close() {
	h.cancelFunc()
	h.resp.Body.Close() // nolint: errcheck
}

func (h *Http1ClientStream) Context() context.Context {
	return h.ctx
}

func (h *Http1ClientStream) SendMsg(m any) error {
	req, err := messageToRequest(h.ctx, h.url, m, h.hdrs)
	if err != nil {
		return err
	}
	resp, err := h.httpClient.Do(req)
	if err != nil {
		return err
	}
	h.resp = resp
	h.storeHeader()
	return nil
}

func (h *Http1ClientStream) storeHeader() {
	h.headers = make(metadata.MD)
	for k, v := range h.resp.Header {
		h.headers[k] = v
	}
	close(h.hasHeader)
}

func (h *Http1ClientStream) RecvMsg(m any) error {
	if h.resp == nil {
		return fmt.Errorf("you must send before receiving")
	}
	h.mutexRecv.Lock()
	defer h.mutexRecv.Unlock()
	codecName := "proto"
	if strings.HasPrefix(h.resp.Header.Get("Content-Type"), "application/grpc-web+") {
		codecName = strings.TrimPrefix(h.resp.Header.Get("Content-Type"), "application/grpc-web+")
	}
	codec := encoding.GetCodec(codecName)
	_, err := bodyToMessage(h.resp.Body, codec, m, true)
	if err != nil {
		return err
	}
	return nil
}
