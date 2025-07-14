/*
** Copyright (C) 2001-2025 Zabbix SIA
**
** This program is free software: you can redistribute it and/or modify it under the terms of
** the GNU Affero General Public License as published by the Free Software Foundation, version 3.
**
** This program is distributed in the hope that it will be useful, but WITHOUT ANY WARRANTY;
** without even the implied warranty of MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.
** See the GNU Affero General Public License for more details.
**
** You should have received a copy of the GNU Affero General Public License along with this program.
** If not, see <https://www.gnu.org/licenses/>.
**/

package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"git.zabbix.com/ZT/kafka-connector/kafka"
	"git.zabbix.com/ap/plugin-support/errs"
	"git.zabbix.com/ap/plugin-support/zbxnet"
	"github.com/google/go-cmp/cmp"
)

var _ kafka.Producer = &mockProducer{}
var _ http.ResponseWriter = &mockWriter{}

type mockProducer struct {
	called   int
	ids      []string
	messages []string
}
type mockWriter struct {
	code     int
	data     []byte
	header   http.Header
	writeErr error
}

func (w *mockWriter) Header() http.Header {
	return w.header
}

func (w *mockWriter) Write(in []byte) (int, error) {
	if w.writeErr != nil {
		return 0, w.writeErr
	}

	w.data = in

	return len(in), nil
}

func (w *mockWriter) WriteHeader(statusCode int) {
	w.code = statusCode
}

func (mp *mockProducer) ProduceItem(key, message string) {
	mp.called++
	mp.ids = append(mp.ids, key)
	mp.messages = append(mp.messages, message)
}

func (mp *mockProducer) ProduceEvent(key, message string) {
	mp.called++
	mp.ids = append(mp.ids, key)
	mp.messages = append(mp.messages, message)
}

func (mp *mockProducer) Close() error {
	return nil
}

func TestBufferedResponseWriter_Write(t *testing.T) {
	t.Parallel()

	type fields struct {
		prevData []byte
	}

	type args struct {
		data []byte
	}

	tests := []struct {
		name      string
		fields    fields
		args      args
		wantCount int
		wantData  string
		wantErr   bool
	}{
		{
			"+valid",
			fields{
				nil,
			},
			args{
				[]byte("body write data"),
			},
			15,
			"body write data",
			false,
		},
		{
			"+prevData",
			fields{
				[]byte("some data, already in buffer\n"),
			},
			args{
				[]byte("body write data"),
			},
			15,
			"some data, already in buffer\nbody write data",
			false,
		},
		{
			"-empty_string",
			fields{
				nil,
			},
			args{
				[]byte(""),
			},
			0,
			"",
			false,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			b := &BufferedResponseWriter{
				buffer: bytes.Buffer{},
			}

			if tt.fields.prevData != nil {
				_, err := b.buffer.Write(tt.fields.prevData)
				if err != nil {
					t.Fatalf("failed to prepare byte buffer with previous data: %s", err.Error())
				}
			}

			got, err := b.Write(tt.args.data)
			if (err != nil) != tt.wantErr {
				t.Fatalf("BufferedResponseWriter.Write() error = %v, wantErr %v", err, tt.wantErr)
			}

			if diff := cmp.Diff(tt.wantCount, got); diff != "" {
				t.Fatalf("BufferedResponseWriter.Write() = %s", diff)
			}

			if diff := cmp.Diff(tt.wantData, b.buffer.String()); diff != "" {
				t.Fatalf("BufferedResponseWriter.Write() = %s", diff)
			}
		})
	}
}

func TestBufferedResponseWriter_WriteResponse(t *testing.T) {
	t.Parallel()

	type fields struct {
		writeErr error
	}

	type args struct {
		header http.Header
		data   string
		code   int
	}

	tests := []struct {
		name       string
		fields     fields
		args       args
		wantCode   int
		wantData   string
		wantHeader http.Header
	}{
		{
			"+valid",
			fields{nil},
			args{
				map[string][]string{
					"Custom": {"header"},
				},
				"write data",
				http.StatusOK,
			},
			http.StatusOK,
			"write data",
			map[string][]string{
				"Custom":                 {"header"},
				"Content-Type":           {"application/json"},
				"X-Content-Type-Options": {"nosniff"},
			},
		},
		{
			"+noCustomHeader",
			fields{nil},
			args{
				nil,
				"write data",
				http.StatusOK,
			},
			http.StatusOK,
			"write data",
			map[string][]string{
				"Content-Type":           {"application/json"},
				"X-Content-Type-Options": {"nosniff"},
			},
		},
		{
			"-writeErr",
			fields{errs.New("write error")},
			args{
				nil,
				"write data",
				http.StatusOK,
			},
			http.StatusOK,
			"",
			map[string][]string{
				"Content-Type":           {"application/json"},
				"X-Content-Type-Options": {"nosniff"},
			},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			w := mockWriter{
				header:   http.Header{},
				writeErr: tt.fields.writeErr,
			}

			b := &BufferedResponseWriter{
				w:      &w,
				buffer: bytes.Buffer{},
				code:   tt.args.code,
				header: tt.args.header,
			}

			_, err := b.buffer.Write([]byte(tt.args.data))
			if err != nil {
				t.Fatalf("failed to write to buf: %s", err.Error())
			}

			b.WriteResponse()

			if diff := cmp.Diff(tt.wantHeader, b.w.Header()); diff != "" {
				t.Fatalf("BufferedResponseWriter.WriteResponse() = %s", diff)
			}

			if w.code != tt.wantCode {
				t.Fatalf(
					"BufferedResponseWriter.WriteResponse() expected status code: %d, but got: %d",
					tt.wantCode,
					w.code,
				)
			}

			if diff := cmp.Diff(tt.wantData, string(w.data)); diff != "" {
				t.Fatalf("BufferedResponseWriter.WriteResponse() = %s", diff)
			}
		})
	}
}

func Test_handler_accessMW(t *testing.T) {
	t.Parallel()

	type fields struct {
		authToken    string
		producer     kafka.Producer
		allowedPeers string
	}

	type args struct {
		reqIp   string
		headers map[string][]string
	}

	tests := []struct {
		name          string
		fields        fields
		args          args
		wantResponse  string
		wantErrString string
		wantCode      int
		wantHandler   bool
	}{
		{
			"+valid",
			fields{
				"",
				nil,
				"127.0.0.1",
			},
			args{
				"127.0.0.1:80",
				map[string][]string{
					"Authorization": {""},
					"Content-Type":  {"application/x-ndjson"},
				},
			},
			"", // successful execution this function does not set a response body
			"",
			http.StatusOK, // successful execution this function does not set a status code, but the default one is 200
			true,
		},
		{
			"+withAuth",
			fields{
				"foobar",
				nil,
				"127.0.0.1",
			},
			args{
				"127.0.0.1:80",
				map[string][]string{
					"Authorization": {"Bearer foobar"},
					"Content-Type":  {"application/x-ndjson"},
				},
			},
			"",
			"",
			http.StatusOK,
			true,
		},
		{
			"+authHeaderIgnored",
			fields{
				"",
				nil,
				"127.0.0.1",
			},
			args{
				"127.0.0.1:80",
				map[string][]string{
					"Authorization": {"Bearer foobar"},
					"Content-Type":  {"application/x-ndjson"},
				},
			},
			"",
			"",
			http.StatusOK,
			true,
		},
		{
			"-ipCheckErr",
			fields{
				"",
				nil,
				"127.0.0.13",
			},
			args{
				"127.0.0.1:80",
				map[string][]string{
					"Authorization": {"Bearer foobar"},
					"Content-Type":  {"application/x-ndjson"},
				},
			},
			"fail",
			"ip validation failed",
			http.StatusForbidden,
			false,
		},
		{
			"-wrongBearerToken",
			fields{
				"barfoo",
				nil,
				"127.0.0.1",
			},
			args{
				"127.0.0.1:80",
				map[string][]string{
					"Authorization": {"Bearer foobar"},
					"Content-Type":  {"application/jibber-jabber"},
				},
			},
			"fail",
			"bearer token validation failed",
			http.StatusUnauthorized,
			false,
		},
		{
			"-unsupportedContentType",
			fields{
				"",
				nil,
				"127.0.0.1",
			},
			args{
				"127.0.0.1:80",
				map[string][]string{
					"Authorization": {"Bearer foobar"},
					"Content-Type":  {"application/jibber-jabber"},
				},
			},
			"fail",
			"header must contain",
			http.StatusUnsupportedMediaType,
			false,
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodPost, "/some/path", nil)
			r.Header = tt.args.headers

			r.RemoteAddr = tt.args.reqIp

			ips, err := zbxnet.GetAllowedPeers(tt.fields.allowedPeers)
			if err != nil {
				t.Fatalf("failed to get allowed peers: %s", err.Error())
			}

			h := &handler{
				authToken:    tt.fields.authToken,
				producer:     tt.fields.producer,
				allowedPeers: ips,
			}

			handlerCalled := false

			handlerFunc := func(http.ResponseWriter, *http.Request) {
				handlerCalled = true
			}

			h.accessMW(handlerFunc)(w, r)

			if w.Code != tt.wantCode {
				t.Fatalf(
					"handler.accessMW() handler expected status code: %d, but got: %d\nresponse body: %s",
					tt.wantCode,
					w.Code,
					w.Body,
				)
			}

			if tt.wantResponse != "" && tt.wantResponse != unmarshalResponse(w.Body)["response"] {
				t.Fatalf(
					"handler.accessMW() handler expected response to contain: '%s', but got full response: '%s'",
					tt.wantResponse,
					w.Body.String(),
				)
			}

			if tt.wantErrString != "" && !strings.Contains(w.Body.String(), tt.wantErrString) {
				t.Fatalf(
					"handler.accessMW() handler expected response error to contain: '%s', but got: '%s'",
					tt.wantErrString,
					w.Body.String(),
				)
			}

			if tt.wantHandler != handlerCalled {
				t.Fatalf(
					"handler.accessMW() handler called status: %t, but got: '%t'",
					tt.wantHandler,
					handlerCalled,
				)
			}
		})
	}
}

func Test_handler_checkIP(t *testing.T) {
	t.Parallel()

	type fields struct {
		allowedPeers string
	}

	type args struct {
		reqIp string
	}

	tests := []struct {
		name    string
		fields  fields
		args    args
		wantErr bool
	}{
		{
			"+valid",
			fields{"127.0.0.1"},
			args{"127.0.0.1:80"},
			false,
		},
		{
			"+multipleIps",
			fields{"127.0.0.1,::1,127.0.0.3"},
			args{"127.0.0.1:80"},
			false,
		},
		{
			"-noRequestPort",
			fields{"127.0.0.1"},
			args{"127.0.0.1"},
			true,
		},
		{
			"-ipNotAllowed",
			fields{"127.0.0.3"},
			args{"127.0.0.1:80"},
			true,
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			r := httptest.NewRequest(http.MethodPost, "/some/path", nil)
			r.RemoteAddr = tt.args.reqIp

			ips, err := zbxnet.GetAllowedPeers(tt.fields.allowedPeers)
			if err != nil {
				t.Fatalf("failed to get allowed peers: %s", err.Error())
			}

			h := &handler{
				allowedPeers: ips,
			}
			if err := h.checkIP(r); (err != nil) != tt.wantErr {
				t.Fatalf("handler.checkIP() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func Test_handler_validateBearerToken(t *testing.T) {
	t.Parallel()

	type fields struct {
		authToken string
	}

	type args struct {
		headers map[string][]string
	}

	tests := []struct {
		name    string
		fields  fields
		args    args
		want    int
		wantErr bool
	}{
		{
			"+valid",
			fields{"foobar"},
			args{
				map[string][]string{
					"Authorization": {"Bearer foobar"},
				},
			},
			0,
			false,
		},
		{
			"-incorrectlyFormattedHeader",
			fields{"foobar"},
			args{
				map[string][]string{
					"Authorization": {"Bearerfoobar"},
				},
			},
			http.StatusBadRequest,
			true,
		},
		{
			"-incorrectToken",
			fields{"foobar"},
			args{
				map[string][]string{
					"Authorization": {"Bearer barfoo"},
				},
			},
			http.StatusUnauthorized,
			true,
		},
		{
			"-missingHeader",
			fields{"foobar"},
			args{
				map[string][]string{},
			},
			http.StatusBadRequest,
			true,
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			r := httptest.NewRequest(http.MethodPost, "/some/path", nil)
			r.Header = tt.args.headers

			h := &handler{
				authToken: tt.fields.authToken,
			}
			got, err := h.validateBearerToken(r)
			if (err != nil) != tt.wantErr {
				t.Fatalf("handler.validateBearerToken() error = %v, wantErr %v", err, tt.wantErr)
			}

			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Fatalf("handler.validateBearerToken() = %s", diff)
			}
		})
	}
}

//nolint:dupl //easier to read and handle the test when duplicated
func Test_handler_events(t *testing.T) {
	t.Parallel()

	type fields struct {
		producer *mockProducer
	}

	type args struct {
		r *http.Request
	}

	tests := []struct {
		name                  string
		fields                fields
		args                  args
		wantResponse          string
		wantCode              int
		wantProducerCallTimes int
		wantIds               []string
		wantMessages          []string
		wantErr               bool
	}{
		{
			"+valid",
			fields{&mockProducer{}},
			args{
				httptest.NewRequest(
					http.MethodPost,
					"/some/path",
					strings.NewReader(
						getRequestString(
							[]map[string]any{
								{"eventid": 23},
								{"eventid": 24},
								{"eventid": 25},
							},
						),
					),
				),
			},
			"success",
			http.StatusCreated,
			3,
			[]string{"23", "24", "25"},
			[]string{
				"{\"eventid\":23}",
				"{\"eventid\":24}",
				"{\"eventid\":25}"},
			false,
		},
		{
			"+validSingle",
			fields{&mockProducer{}},
			args{
				httptest.NewRequest(
					http.MethodPost,
					"/some/path",
					strings.NewReader(
						getRequestString(
							[]map[string]any{
								{"eventid": 25},
							},
						),
					),
				),
			},
			"success",
			http.StatusCreated,
			1,
			[]string{"25"},
			[]string{"{\"eventid\":25}"},
			false,
		},
		{
			"-emptyBody",
			fields{&mockProducer{}},
			args{
				httptest.NewRequest(http.MethodPost, "/some/path", nil),
			},
			"",            // body not set on fails
			http.StatusOK, // default, function does not set code on fail
			0,
			nil,
			nil,
			true,
		},
		{
			"-invalidBody",
			fields{&mockProducer{}},
			args{
				httptest.NewRequest(http.MethodPost, "/some/path", strings.NewReader("{eventid:wqe}")),
			},
			"",
			http.StatusOK,
			0,
			nil,
			nil,
			true,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			w := httptest.NewRecorder()

			h := handler{
				producer: tt.fields.producer,
			}
			if err := h.events(w, tt.args.r); (err != nil) != tt.wantErr {
				t.Fatalf("handler.events() error = %v, wantErr %v", err, tt.wantErr)
			}

			if w.Code != tt.wantCode {
				t.Fatalf("handler.events() expected status code: `%d`, but got: `%d`", tt.wantCode, w.Code)
			}

			if tt.wantResponse != "" && tt.wantResponse != unmarshalResponse(w.Body)["response"] {
				t.Fatalf(
					"handler.events() handler expected response to contain: '%s', but got full response: '%s'",
					tt.wantResponse,
					w.Body.String(),
				)
			}

			if tt.wantProducerCallTimes != tt.fields.producer.called {
				t.Fatalf(
					"handler.events() expected handler called time: `%d`, but got called: '%d' times",
					tt.wantProducerCallTimes,
					tt.fields.producer.called,
				)
			}

			if diff := cmp.Diff(tt.wantIds, tt.fields.producer.ids); diff != "" {
				t.Fatalf(
					"handler.events() expected handler called with: `%s` ids, but got called with: '%s' ids",
					tt.wantIds,
					tt.fields.producer.ids,
				)
			}

			if diff := cmp.Diff(tt.wantMessages, tt.fields.producer.messages); diff != "" {
				t.Fatalf(
					"handler.events() expected handler called with: `%s` messages, but got called with: '%s' messages",
					tt.wantMessages,
					tt.fields.producer.messages,
				)
			}
		})
	}
}

//nolint:dupl //easier to read and handle the test when duplicated
func Test_handler_items(t *testing.T) {
	t.Parallel()

	type fields struct {
		producer *mockProducer
	}

	type args struct {
		r *http.Request
	}

	tests := []struct {
		name                  string
		fields                fields
		args                  args
		wantResponse          string
		wantCode              int
		wantProducerCallTimes int
		wantIds               []string
		wantMessages          []string
		wantErr               bool
	}{
		{
			"+valid",
			fields{&mockProducer{}},
			args{
				httptest.NewRequest(
					http.MethodPost,
					"/some/path",
					strings.NewReader(
						getRequestString(
							[]map[string]any{
								{"itemid": 23},
								{"itemid": 24},
								{"itemid": 25},
							},
						),
					),
				),
			},
			"success",
			http.StatusCreated,
			3,
			[]string{"23", "24", "25"},
			[]string{
				"{\"itemid\":23}",
				"{\"itemid\":24}",
				"{\"itemid\":25}",
			},
			false,
		},
		{
			"+validSingle",
			fields{&mockProducer{}},
			args{
				httptest.NewRequest(
					http.MethodPost,
					"/some/path",
					strings.NewReader(
						getRequestString(
							[]map[string]any{
								{"itemid": 25},
							},
						),
					),
				),
			},
			"success",
			http.StatusCreated,
			1,
			[]string{"25"},
			[]string{"{\"itemid\":25}"},
			false,
		},
		{
			"-emptyBody",
			fields{&mockProducer{}},
			args{
				httptest.NewRequest(http.MethodPost, "/some/path", nil),
			},
			"",            // body not set on fails
			http.StatusOK, // default, function does not set code on fail
			0,
			nil,
			nil,
			true,
		},
		{
			"-invalidBody",
			fields{&mockProducer{}},
			args{
				httptest.NewRequest(http.MethodPost, "/some/path", strings.NewReader("{itemid:wqe}")),
			},
			"",
			http.StatusOK,
			0,
			nil,
			nil,
			true,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			w := httptest.NewRecorder()

			h := handler{
				producer: tt.fields.producer,
			}

			if err := h.items(w, tt.args.r); (err != nil) != tt.wantErr {
				t.Fatalf("handler.items() error = %v, wantErr %v", err, tt.wantErr)
			}

			if w.Code != tt.wantCode {
				t.Fatalf("handler.items() expected status code: `%d`, but got: `%d`", tt.wantCode, w.Code)
			}

			if tt.wantResponse != "" && tt.wantResponse != unmarshalResponse(w.Body)["response"] {
				t.Fatalf(
					"handler.items() handler expected response to contain: '%s', but got full response: '%s'",
					tt.wantResponse,
					w.Body.String(),
				)
			}

			if tt.wantProducerCallTimes != tt.fields.producer.called {
				t.Fatalf(
					"handler.items() expected handler called time: `%d`, but got called: '%d' times",
					tt.wantProducerCallTimes,
					tt.fields.producer.called,
				)
			}

			if diff := cmp.Diff(tt.wantIds, tt.fields.producer.ids); diff != "" {
				t.Fatalf(
					"handler.items() expected handler called with: `%s` ids, but got called with: '%s' ids",
					tt.wantIds,
					tt.fields.producer.ids,
				)
			}

			if diff := cmp.Diff(tt.wantMessages, tt.fields.producer.messages); diff != "" {
				t.Fatalf(
					"handler.items() expected handler called with: `%s` messages, but got called with: '%s' messages",
					tt.wantMessages,
					tt.fields.producer.messages,
				)
			}
		})
	}
}

func Test_notFoundMW(t *testing.T) {
	t.Parallel()

	type args struct {
		code int
	}

	tests := []struct {
		name          string
		args          args
		wantResponse  string
		wantErrString string
		wantCode      int
	}{
		{
			"+valid",
			args{http.StatusOK},
			"",
			"",
			http.StatusOK,
		},
		{
			"-notFound",
			args{http.StatusNotFound},
			"fail",
			"Not Found",
			http.StatusNotFound,
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodPost, "/some/path", nil)

			notFoundMW(http.HandlerFunc(
				func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(tt.args.code)
					_, err := w.Write([]byte("success"))
					if err != nil {
						panic(fmt.Sprintf("failed to write on response: %s", err.Error()))
					}
				},
			)).ServeHTTP(w, r)

			if w.Code != tt.wantCode {
				t.Fatalf(
					"notFoundMW().ServeHTTP() expected status code: %d, but got: %d\nresponse body: %s",
					tt.wantCode,
					w.Code,
					w.Body,
				)
			}

			if tt.wantResponse != "" && tt.wantResponse != unmarshalResponse(w.Body)["response"] {
				t.Fatalf(
					"notFoundMW().ServeHTTP() handler expected response to contain: '%s', but got full response: '%s'",
					tt.wantResponse,
					w.Body.String(),
				)
			}

			if tt.wantErrString != "" && !strings.Contains(w.Body.String(), tt.wantErrString) {
				t.Fatalf(
					"notFoundMW().ServeHTTP() expected response error to contain: '%s', but got: '%s'",
					tt.wantErrString,
					w.Body.String(),
				)
			}
		})
	}
}

func Test_allowedMethodsMW(t *testing.T) {
	t.Parallel()

	type args struct {
		allowedMethods []string
	}

	tests := []struct {
		name          string
		args          args
		wantResponse  string
		wantErrString string
		wantCode      int
		wantHandler   bool
	}{
		{
			"+valid",
			args{[]string{"POST"}},
			"",
			"",
			http.StatusOK,
			true,
		},
		{
			"+multipleMethods",
			args{[]string{"POST", "GET", "PATCH", "DELETE", "PUT"}},
			"",
			"",
			http.StatusOK,
			true,
		},
		{
			"-methodNowAllowed",
			args{[]string{"GET"}},
			"fail",
			"Method Not Allowed",
			http.StatusMethodNotAllowed,
			false,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodPost, "/some/path", nil)
			handlerCalled := false

			handlerFunc := func(http.ResponseWriter, *http.Request) {
				handlerCalled = true
			}

			allowedMethodsMW(tt.args.allowedMethods, handlerFunc)(w, r)

			if w.Code != tt.wantCode {
				t.Fatalf(
					"allowedMethodsMW()() expected status code: %d, but got: %d\nresponse body: %s",
					tt.wantCode,
					w.Code, w.Body,
				)
			}

			if tt.wantResponse != "" && tt.wantResponse != unmarshalResponse(w.Body)["response"] {
				t.Fatalf(
					"allowedMethodsMW()() handler expected response to contain: '%s', but got full response: '%s'",
					tt.wantResponse,
					w.Body.String(),
				)
			}

			if tt.wantErrString != "" && !strings.Contains(w.Body.String(), tt.wantErrString) {
				t.Fatalf(
					"allowedMethodsMW()() expected response error to contain: '%s', but got: '%s'",
					tt.wantErrString,
					w.Body.String(),
				)
			}

			if tt.wantHandler != handlerCalled {
				t.Fatalf(
					"allowedMethodsMW()() expected handler called status: %t, but got: '%t'",
					tt.wantHandler,
					handlerCalled,
				)
			}
		})
	}
}

func Test_errorHandlingMW(t *testing.T) {
	t.Parallel()

	type args struct {
		returnErr bool
	}

	tests := []struct {
		name          string
		args          args
		wantResponse  string
		wantErrString string
		wantCode      int
	}{
		{
			"+valid",
			args{false},
			"",
			"",
			http.StatusOK,
		},
		{
			"-errorResponse",
			args{true},
			"fail",
			"Handler error.",
			http.StatusInternalServerError,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodPost, "/some/path", nil)

			handlerFunc := func(http.ResponseWriter, *http.Request) error {
				if tt.args.returnErr {
					return errs.New("handler error")
				}

				return nil
			}

			errorHandlingMW(handlerFunc)(w, r)

			if w.Code != tt.wantCode {
				t.Fatalf(
					"errorHandlingMW()() expected status code: %d, but got: %d\nresponse body: %s",
					tt.wantCode,
					w.Code,
					w.Body,
				)
			}

			if tt.wantResponse != "" && tt.wantResponse != unmarshalResponse(w.Body)["response"] {
				t.Fatalf(
					"errorHandlingMW()() handler expected response to contain: '%s', but got full response: '%s'",
					tt.wantResponse,
					w.Body.String(),
				)
			}

			if tt.wantErrString != "" && !strings.Contains(w.Body.String(), tt.wantErrString) {
				t.Fatalf(
					"errorHandlingMW()() expected response error to contain: '%s', but got: '%s'",
					tt.wantErrString,
					w.Body.String(),
				)
			}
		})
	}
}

func Test_write(t *testing.T) {
	t.Parallel()

	type args struct {
		status  int
		message string
	}

	tests := []struct {
		name         string
		args         args
		wantCode     int
		wantResponse string
	}{
		{
			"+valid",
			args{
				http.StatusOK,
				"success",
			},
			http.StatusOK,
			"success",
		},
		{
			"-empty",
			args{
				http.StatusBadRequest,
				"",
			},
			http.StatusBadRequest,
			"",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			w := httptest.NewRecorder()

			write(w, tt.args.status, tt.args.message)

			if w.Code != tt.wantCode {
				t.Fatalf(
					"write() expected status code: %d, but got: %d\nresponse body: %s", tt.wantCode, w.Code, w.Body,
				)
			}

			if tt.wantResponse != "" && !strings.Contains(w.Body.String(), tt.wantResponse) {
				t.Fatalf(
					"write() expected response to contain: '%s', but got: '%s'", tt.wantResponse, w.Body.String(),
				)
			}
		})
	}
}

func Test_decodeEvents(t *testing.T) {
	t.Parallel()

	type args struct {
		events string
	}

	tests := []struct {
		name    string
		args    args
		want    []event
		wantErr bool
	}{
		{
			"+valid",
			args{
				getRequestString(
					[]map[string]any{
						{
							"host":    []string{"host_one", "host__two"},
							"eventid": 23,
							"name":    "event_one",
						},
						{
							"host":    []string{"host_three", "host_four"},
							"eventid": 24,
							"name":    "event_two",
						},
						{
							"host":    []string{"host_five", "host_six"},
							"eventid": 25,
							"name":    "event_three",
						},
					},
				),
			},
			[]event{
				{23, `{"eventid":23,"host":["host_one","host__two"],"name":"event_one"}`},
				{24, `{"eventid":24,"host":["host_three","host_four"],"name":"event_two"}`},
				{25, `{"eventid":25,"host":["host_five","host_six"],"name":"event_three"}`},
			},
			false,
		},
		{
			"+single",
			args{
				getRequestString(
					[]map[string]any{
						{
							"host":    []string{"foo", "bar"},
							"eventid": 23,
							"name":    "Foobar",
						},
					},
				),
			},
			[]event{
				{23, `{"eventid":23,"host":["foo","bar"],"name":"Foobar"}`},
			},
			false,
		},
		{
			"-malformedBody",
			args{"\"malformed"},
			nil,
			true,
		},
		{
			"-unmarshalErr",
			args{`"invalid":21`},
			nil,
			true,
		},
		{
			"-empty",
			args{},
			nil,
			false,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := decodeEvents(strings.NewReader(tt.args.events))
			if (err != nil) != tt.wantErr {
				t.Fatalf("decodeEvents() error = %v, wantErr %v", err, tt.wantErr)
			}

			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Fatalf("decodeEvents() = %s", diff)
			}
		})
	}
}

func Test_decodeItems(t *testing.T) {
	t.Parallel()

	type args struct {
		items string
	}

	tests := []struct {
		name    string
		args    args
		want    []item
		wantErr bool
	}{
		{
			"+valid",
			args{
				getRequestString(
					[]map[string]any{
						{
							"host":   []string{"host_one", "host_two"},
							"itemid": 23,
							"name":   "item_one",
						},
						{
							"host":   []string{"host_three", "host_four"},
							"itemid": 24,
							"name":   "item_two",
						},
						{
							"host":   []string{"host_five", "host_six"},
							"itemid": 25,
							"name":   "item_three",
						},
					},
				),
			},
			[]item{
				{23, `{"host":["host_one","host_two"],"itemid":23,"name":"item_one"}`},
				{24, `{"host":["host_three","host_four"],"itemid":24,"name":"item_two"}`},
				{25, `{"host":["host_five","host_six"],"itemid":25,"name":"item_three"}`},
			},
			false,
		},
		{
			"+single",
			args{
				getRequestString(
					[]map[string]any{
						{
							"itemid": 23,
							"host":   []string{"foo", "bar"},
							"name":   "Foobar",
						},
					},
				),
			},
			[]item{
				{23, `{"host":["foo","bar"],"itemid":23,"name":"Foobar"}`},
			},
			false,
		},
		{
			"-malformedBody",
			args{"\"malformed"},
			nil,
			true,
		},
		{
			"-unmarshalErr",
			args{`"invalid":21`},
			nil,
			true,
		},
		{
			"-empty",
			args{},
			nil,
			false,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := decodeItems(strings.NewReader(tt.args.items))
			if (err != nil) != tt.wantErr {
				t.Fatalf("decodeItems() error = %v, wantErr %v", err, tt.wantErr)
			}

			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Fatalf("decodeItems() = %s", diff)
			}
		})
	}
}

func Test_validateTLS(t *testing.T) {
	t.Parallel()

	type args struct {
		certPath string
		keyPath  string
	}

	tests := []struct {
		name    string
		args    args
		wantErr bool
	}{
		{
			"+valid",
			args{
				"path/to/cert",
				"path/to/key",
			},
			false,
		},
		{
			"-missingKey",
			args{
				"path/to/cert",
				"",
			},
			true,
		},
		{
			"-missingCert",
			args{
				"",
				"path/to/key",
			},
			true,
		},
		{
			"-missingBoth",
			args{
				"",
				"",
			},
			true,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if err := validateTLS(tt.args.certPath, tt.args.keyPath); (err != nil) != tt.wantErr {
				t.Fatalf("validateTLS() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func Test_jsonResponse(t *testing.T) {
	t.Parallel()

	type args struct {
		msg map[string]string
	}

	tests := []struct {
		name string
		args args
		want string
	}{
		{
			"+valid",
			args{
				map[string]string{
					"single": "message",
				},
			},
			`{"single":"message"}`,
		},
		{
			"+multiple",
			args{
				map[string]string{
					"first":  "foo",
					"second": "bar",
				},
			},
			`{"first":"foo","second":"bar"}`,
		},
		{
			"-nil",
			args{
				nil,
			},
			"null",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := jsonResponse(tt.args.msg)
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Fatalf("jsonResponse() = %s", diff)
			}
		})
	}
}

func getRequestString(data []map[string]any) string {
	ndjson := new(bytes.Buffer)

	encoder := json.NewEncoder(ndjson)

	for _, e := range data {
		err := encoder.Encode(e)
		if err != nil {
			panic(fmt.Sprintf("failed to prepare unit test values: %s", err.Error()))
		}
	}

	return ndjson.String()
}

func unmarshalResponse(b *bytes.Buffer) map[string]string {
	out := map[string]string{}

	err := json.Unmarshal(b.Bytes(), &out)
	if err != nil {
		panic(fmt.Sprintf("failed to unmarshal response: %s", err.Error()))
	}

	return out
}
