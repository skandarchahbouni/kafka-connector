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

package kafka

import (
	"crypto/tls"
	"testing"
	"time"
)

//nolint:gocognit,gocyclo,cyclop // requires a lot of config field checks
func Test_newConfig(t *testing.T) {
	t.Parallel()

	type args struct {
		username  string
		password  string
		retries   int
		tlsAuth   bool
		enableTLS bool
		timeout   time.Duration
		keepAlive time.Duration
		tlsConf   *tls.Config
	}

	tests := []struct {
		name                       string
		args                       args
		wantClientID               string
		wantUsername               string
		wantPassword               string
		wantKeepAlive              time.Duration
		wantDialTimeout            time.Duration
		wantReadTimeout            time.Duration
		wantWriteTimeout           time.Duration
		wantRetryMax               int
		wantSASLEnable             bool
		wantTLSEnable              bool
		wantAllowAutoTopicCreation bool
		wantTlsConfNotNil          bool
		wantTlsAuthEnable          bool
	}{
		{
			"+valid",
			args{
				"",
				"",
				2,
				false,
				false,
				3,
				30,
				nil,
			},
			"zabbix",
			"",
			"",
			30,
			3,
			3,
			3,
			2,
			false,
			false,
			false,
			false,
			false,
		},
		{
			"+SASL",
			args{
				"username",
				"password",
				2,
				false,
				false,
				3,
				30,
				nil,
			},
			"zabbix",
			"username",
			"password",
			30,
			3,
			3,
			3,
			2,
			true,
			false,
			false,
			false,
			false,
		},
		{
			"+TLS",
			args{
				"",
				"",
				2,
				true,
				false,
				3,
				30,
				&tls.Config{ServerName: "127.0.0.1"}, //nolint:gosec // struct fields are not used
			},
			"zabbix",
			"",
			"",
			30,
			3,
			3,
			3,
			2,
			false,
			true,
			false,
			true,
			true,
		},
		{
			"+Full",
			args{
				"foo",
				"bar",
				2,
				true,
				true,
				3,
				30,
				&tls.Config{ServerName: "127.0.0.1"}, //nolint:gosec // struct fields are not used
			},
			"zabbix",
			"foo",
			"bar",
			30,
			3,
			3,
			3,
			2,
			true,
			true,
			false,
			true,
			true,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := newConfig(
				tt.args.username,
				tt.args.password,
				tt.args.retries,
				tt.args.tlsAuth,
				tt.args.enableTLS,
				tt.args.timeout,
				tt.args.keepAlive,
				tt.args.tlsConf,
			)

			if tt.wantClientID != got.ClientID {
				t.Fatalf("newConfig() expected ClientId: '%s', but got: '%s'", tt.wantClientID, got.ClientID)
			}

			if tt.wantUsername != got.Net.SASL.User {
				t.Fatalf("newConfig() expected SASL user: '%s', but got: '%s'", tt.wantUsername, got.Net.SASL.User)
			}

			if tt.wantPassword != got.Net.SASL.Password {
				t.Fatalf(
					"newConfig() expected SASL Password: '%s', but got: '%s'",
					tt.wantPassword,
					got.Net.SASL.Password,
				)
			}

			if (tt.wantTlsConfNotNil && got.Net.TLS.Config == nil) ||
				(!tt.wantTlsConfNotNil && got.Net.TLS.Config != nil) {
				t.Fatalf(
					"newConfig() expected TLS config to be set: %t, but got: '%v'",
					tt.wantTlsConfNotNil,
					got.Net.TLS.Config,
				)
			}

			if tt.wantTlsAuthEnable != got.Net.TLS.Enable {
				t.Fatalf(
					"newConfig() expected TLS auth: '%t', but got: '%t'",
					tt.wantTlsAuthEnable,
					got.Net.TLS.Enable,
				)
			}

			if tt.wantKeepAlive != got.Net.KeepAlive {
				t.Fatalf("newConfig() expected KeepAlive: '%s', but got: '%s'",
					tt.wantKeepAlive,
					got.Net.KeepAlive,
				)
			}

			if tt.wantDialTimeout != got.Net.DialTimeout {
				t.Fatalf("newConfig() expected DialTimeout: '%s', but got: '%s'",
					tt.wantDialTimeout,
					got.Net.DialTimeout,
				)
			}

			if tt.wantReadTimeout != got.Net.ReadTimeout {
				t.Fatalf(
					"newConfig() expected ReadTimeout: '%s', but got: '%s'",
					tt.wantReadTimeout,
					got.Net.ReadTimeout,
				)
			}

			if tt.wantWriteTimeout != got.Net.WriteTimeout {
				t.Fatalf(
					"newConfig() expected WriteTimeout: '%s', but got: '%s'",
					tt.wantWriteTimeout,
					got.Net.WriteTimeout,
				)
			}

			if tt.wantRetryMax != got.Producer.Retry.Max {
				t.Fatalf("newConfig() expected RetryMax: '%d', but got: '%d'",
					tt.wantRetryMax,
					got.Producer.Retry.Max,
				)
			}

			if tt.wantSASLEnable != got.Net.SASL.Enable {
				t.Fatalf(
					"newConfig() expected SASLEnable: '%t', but got: '%t'",
					tt.wantSASLEnable,
					got.Net.SASL.Enable,
				)
			}

			if tt.wantTLSEnable != got.Net.TLS.Enable {
				t.Fatalf("newConfig() expected RetryMax: '%t', but got: '%t'", tt.wantTLSEnable, got.Net.TLS.Enable)
			}

			if tt.wantAllowAutoTopicCreation != got.Metadata.AllowAutoTopicCreation {
				t.Fatalf(
					"newConfig() expected AllowAutoTopicCreation: '%t', but got: '%t'",
					tt.wantAllowAutoTopicCreation,
					got.Metadata.AllowAutoTopicCreation,
				)
			}
		})
	}
}
