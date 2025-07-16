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
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"git.zabbix.com/ZT/kafka-connector/kafka"
	"git.zabbix.com/ap/plugin-support/errs"
	"git.zabbix.com/ap/plugin-support/log"
	"git.zabbix.com/ap/plugin-support/zbxnet"
)

const (
	contentType        = "Content-Type"
	applicationXndJSON = "application/x-ndjson"
	applicationJSON    = "application/json"
)

var _ http.ResponseWriter = &BufferedResponseWriter{}

// BufferedResponseWriter response writer for http handler.
type BufferedResponseWriter struct {
	w      http.ResponseWriter
	buffer bytes.Buffer
	code   int
	header http.Header
}
type handler struct {
	authToken    string
	producer     kafka.Producer
	allowedPeers *zbxnet.AllowedPeers
}

type event struct {
	EventID int    `json:"eventid"`
	Data    string `json:"data"`
}

type item struct {
	ItemID int    `json:"itemid"`
	Data   string `json:"data"`
}

// ServerInit initializes a http server with provided parameters.
func ServerInit(port string, router http.Handler, timeout int) *http.Server {
	return &http.Server{
		Addr:              fmt.Sprintf(":%s", port),
		Handler:           router,
		ReadHeaderTimeout: time.Duration(timeout) * time.Second,
	}
}

// Run starts the server.
func Run(server *http.Server, cert, key string, tls bool, errors chan<- error) {
	if tls {
		runTLS(server, cert, key, errors)

		return
	}

	run(server, errors)
}

// NewRouter creates a mux http handler with all the routing handled.
func NewRouter(producer *kafka.DefaultProducer, auth string, allowedIPs *zbxnet.AllowedPeers) http.Handler {
	router := http.NewServeMux()

	h := handler{
		authToken:    auth,
		producer:     producer,
		allowedPeers: allowedIPs,
	}

	router.HandleFunc(
		"/api/v1/events",
		allowedMethodsMW(
			[]string{http.MethodPost},
			h.accessMW(
				errorHandlingMW(h.events),
			),
		),
	)

	router.HandleFunc(
		"/api/v1/items",
		allowedMethodsMW(
			[]string{http.MethodPost},
			h.accessMW(
				errorHandlingMW(h.items),
			),
		),
	)

	return notFoundMW(router)
}

// Header returns set headers.
func (b *BufferedResponseWriter) Header() http.Header {
	return b.header
}

// Write writes data into buffer.
func (b *BufferedResponseWriter) Write(data []byte) (int, error) {
	n, err := b.buffer.Write(data)
	if err != nil {
		return n, errs.Wrap(err, "failed to write data to response writer")
	}

	return n, nil
}

// WriteHeader sets code for response.
func (b *BufferedResponseWriter) WriteHeader(code int) {
	b.code = code
}

// WriteResponse writes response as a json encoded text.
func (b *BufferedResponseWriter) WriteResponse() {
	b.w.Header().Set("Content-Type", applicationJSON)
	b.w.Header().Set("X-Content-Type-Options", "nosniff")
	b.w.WriteHeader(b.code)

	for k, v := range b.header {
		for _, vv := range v {
			b.w.Header().Add(k, vv)
		}
	}

	_, err := b.w.Write(b.buffer.Bytes())
	if err != nil {
		log.Errf("failed to write response %s", err)
	}
}

//nolint:revive // checks 3 things no reason to split up because of complexity
func (h *handler) accessMW(handler http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		err := h.checkIP(r)
		if err != nil {
			write(
				w,
				http.StatusForbidden,
				jsonResponse(
					map[string]string{
						"response": "fail",
						"error":    fmt.Sprintf("ip validation failed, %s", err.Error()),
					},
				),
			)

			return
		}

		if h.authToken != "" {
			code, err := h.validateBearerToken(r)
			if err != nil {
				write(
					w,
					code,
					jsonResponse(
						map[string]string{
							"response": "fail",
							"error":    fmt.Sprintf("bearer token validation failed, %s", err.Error()),
						},
					),
				)

				return
			}
		}

		ct := r.Header.Get(contentType)
		if ct != "" && ct != applicationXndJSON {
			write(
				w,
				http.StatusUnsupportedMediaType,
				jsonResponse(
					map[string]string{
						"response": "fail",
						"error":    fmt.Sprintf("%s header must contain %s", contentType, applicationXndJSON),
					},
				),
			)

			return
		}

		handler(w, r)
	}
}

func (h *handler) checkIP(req *http.Request) error {
	host, _, err := net.SplitHostPort(req.RemoteAddr)
	if err != nil {
		return errs.Wrap(err, "failed to split request ip and port")
	}

	if !h.allowedPeers.CheckPeer(net.ParseIP(host)) {
		return errs.New("ip not allowed")
	}

	return nil
}

func (h *handler) validateBearerToken(r *http.Request) (int, error) {
	splitToken := strings.Split(r.Header.Get("Authorization"), "Bearer ")

	if len(splitToken) < 2 {
		return http.StatusBadRequest, errs.New("failed to retrieve bearer auth token")
	}

	if h.authToken != splitToken[1] {
		return http.StatusUnauthorized, errs.New("incorrect bearer auth token")
	}

	return 0, nil
}

func (h handler) events(w http.ResponseWriter, r *http.Request) error {
	events, err := decodeEvents(r.Body)
	if err != nil {
		return errs.Wrap(err, "failed to read request")
	}

	if len(events) == 0 {
		return errs.New("empty request")
	}

	for _, v := range events {
		h.producer.ProduceEvent(strconv.Itoa(v.EventID), v.Data)
	}

	write(
		w,
		http.StatusCreated,
		jsonResponse(
			map[string]string{
				"response": "success",
			},
		),
	)

	return nil
}

func (h handler) items(w http.ResponseWriter, r *http.Request) error {
	items, err := decodeItems(r.Body)
	if err != nil {
		return errs.Wrap(err, "failed to read request")
	}

	if len(items) == 0 {
		return errs.New("empty request")
	}

	for _, v := range items {
		h.producer.ProduceItem(strconv.Itoa(v.ItemID), v.Data)
	}

	write(
		w,
		http.StatusCreated,
		jsonResponse(
			map[string]string{
				"response": "success",
			},
		),
	)

	return nil
}

func notFoundMW(handler http.Handler) http.Handler {
	return http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			bw := &BufferedResponseWriter{
				w:      w,
				header: http.Header{},
				code:   http.StatusOK,
			}

			handler.ServeHTTP(bw, r)

			if bw.code == http.StatusNotFound {
				write(
					w,
					http.StatusNotFound,
					jsonResponse(
						map[string]string{
							"response": "fail",
							"error":    http.StatusText(http.StatusNotFound),
						},
					),
				)

				return
			}

			bw.WriteResponse()
		},
	)
}

func allowedMethodsMW(allowedMethods []string, handler http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		for _, method := range allowedMethods {
			if r.Method == method {
				handler(w, r)

				return
			}
		}

		write(
			w,
			http.StatusMethodNotAllowed,
			jsonResponse(
				map[string]string{
					"response": "fail",
					"error":    http.StatusText(http.StatusMethodNotAllowed),
				},
			),
		)
	}
}

func errorHandlingMW(
	handler func(w http.ResponseWriter, r *http.Request) error,
) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		err := handler(w, r)
		if err != nil {
			log.Errf("failed handle request, %s", err.Error())

			write(
				w,
				http.StatusInternalServerError,
				jsonResponse(
					map[string]string{
						"response": "fail",
						"error":    err.Error(),
					},
				),
			)
		}
	}
}

func write(w http.ResponseWriter, status int, message string) {
	w.WriteHeader(status)

	_, err := w.Write([]byte(message))
	if err != nil {
		log.Errf("failed to write response, %s", err)
	}
}

func decodeEvents(r io.Reader) ([]event, error) {
	var (
		d      any
		events []event
	)

	decoder := json.NewDecoder(r)

	for decoder.More() {
		err := decoder.Decode(&d)
		if err != nil {
			return nil, errs.Wrap(err, "failed to decode incoming item data")
		}

		b, err := json.Marshal(d)
		if err != nil {
			return nil, errs.Wrap(err, "failed to marshal incoming item data")
		}

		var e event

		err = json.Unmarshal(b, &e)
		if err != nil {
			return nil, errs.Wrap(err, "failed to unmarshal incoming item data")
		}

		e.Data = string(b)

		log.Tracef("Received event with ID %d", e.EventID)

		events = append(events, e)
	}

	return events, nil
}

func filterItemData(rawJSON string, fieldsToKeep []string) (string, error) {
	// Unmarshal into a generic map
	var dataMap map[string]interface{}
	err := json.Unmarshal([]byte(rawJSON), &dataMap)
	if err != nil {
		return "", err
	}

	// Create a new map with only wanted fields
	filtered := make(map[string]interface{})
	for _, field := range fieldsToKeep {
		if val, ok := dataMap[field]; ok {
			filtered[field] = val
		}
	}

	// Marshal filtered map back to JSON string
	filteredJSON, err := json.Marshal(filtered)
	if err != nil {
		return "", err
	}

	return string(filteredJSON), nil
}

func decodeItems(r io.Reader) ([]item, error) {
	var (
		d     any
		items []item
	)

	decoder := json.NewDecoder(r)

	for decoder.More() {
		err := decoder.Decode(&d)
		if err != nil {
			return nil, errs.Wrap(err, "failed to decode incoming item data")
		}

		b, err := json.Marshal(d)
		if err != nil {
			return nil, errs.Wrap(err, "failed to marshal incoming item data")
		}

		var i item

		err = json.Unmarshal(b, &i)
		if err != nil {
			return nil, errs.Wrap(err, "failed to unmarshal incoming item data")
		}

		i.Data = string(b)

		log.Tracef("Received item with ID %d", i.ItemID)


		fieldsToKeep := []string{"itemid", "name", "value"}  // fields you want to preserve

		filteredData, err := filterItemData(i.Data, fieldsToKeep)
		if err != nil {
			log.Errf("failed to filter item data: %s", err)
			// handle error, maybe skip this item or return error
		} else {
			i.Data = filteredData
		}

		items = append(items, i)
	}

	return items, nil
}

func run(server *http.Server, e chan<- error) {
	err := server.ListenAndServe()
	if err != nil {
		e <- errs.Wrap(err, "failed to start the server")
	}
}

func runTLS(server *http.Server, cert, key string, e chan<- error) {
	err := validateTLS(cert, key)
	if err != nil {
		e <- errs.Wrap(err, "failed to start the server")

		return
	}

	err = server.ListenAndServeTLS(cert, key)
	if err != nil {
		e <- errs.Wrap(err, "failed to start the server")
	}
}

func validateTLS(certPath, keyPath string) error {
	if certPath == "" || keyPath == "" {
		return errs.New("both tls certificate and key file paths must be set")
	}

	return nil
}

func jsonResponse(msg map[string]string) string {
	out, err := json.Marshal(msg)
	if err != nil {
		log.Errf("failed to create json response, %s", err.Error())

		return "{}"
	}

	return string(out)
}
