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
	"time"
	"strings"
	"git.zabbix.com/ap/plugin-support/errs"
	"git.zabbix.com/ap/plugin-support/log"
	"git.zabbix.com/ap/plugin-support/tlsconfig"
	"github.com/IBM/sarama"
)

const (
	clientID = "zabbix"
)

// Producer defines requirements for Kafka producer.
type Producer interface {
	ProduceItem(key, message string)
	ProduceEvent(key, message string)
	Close() error
}

// DefaultProducer produces data to Kafka broker.
type DefaultProducer struct {
	eventsTopic string
	itemsTopic  string
	async       sarama.AsyncProducer
	timeout     time.Duration
}

// Configuration hold kafka configuration tags bases on Zabbix configuration package from plugin support.
type Configuration struct {
	Brokers        string `conf:"default=localhost:9092"` // Comma-separated list
	Events         string `conf:"default=events"`
	Items          string `conf:"default=items"`
	KeepAlive      int    `conf:"range=60:300,default=300"`
	Username       string `conf:"optional"`
	Password       string `conf:"optional"`
	CaFile         string `conf:"optional"`
	ClientCertFile string `conf:"optional"`
	ClientKeyFile  string `conf:"optional"`
	Retry          int    `conf:"default=0"`
	Timeout        int    `conf:"default=1"`
	TLSAuth        bool   `conf:"default=false"`
	EnableTLS      bool   `conf:"optional"`
}


// ProduceItem produces Kafka message to the item topic
// in the broker provided in the async producer.
func (p *DefaultProducer) ProduceItem(key, message string) {
	m := &sarama.ProducerMessage{
		Topic: p.itemsTopic,
		Key:   sarama.StringEncoder(key),
		Value: sarama.StringEncoder(message),
	}

	p.produce(m)
}

// ProduceEvent produces Kafka message to the event topic
// in the broker provided in the async producer.
func (p *DefaultProducer) ProduceEvent(key, message string) {
	m := &sarama.ProducerMessage{
		Topic: p.eventsTopic,
		Key:   sarama.StringEncoder(key),
		Value: sarama.StringEncoder(message),
	}

	p.produce(m)
}

// Close closes the underlying async producer.
func (p *DefaultProducer) Close() error {
	err := p.async.Close()
	if err != nil {
		return errs.Wrap(err, "failed to close Kafka async producer")
	}

	return nil
}

// NewProducer creates Kafka producers from with provided configuration.
func NewProducer(c *Configuration) (*DefaultProducer, error) {
	brokers := strings.Split(c.Brokers, ",")
	for i := range brokers {
		brokers[i] = strings.TrimSpace(brokers[i])
	}

	var tlsConfig *tls.Config
	var err error

	if c.TLSAuth {
		// Just use the first broker to generate the TLS config
		tlsConfig, err = getTLSConf(brokers[0], c.CaFile, c.ClientCertFile, c.ClientKeyFile)
		if err != nil {
			return nil, errs.Wrap(err, "failed get TLS config")
		}
	}

	kconf := newConfig(
		c.Username,
		c.Password,
		c.Retry,
		c.TLSAuth,
		c.EnableTLS,
		time.Duration(c.Timeout)*time.Second,
		time.Duration(c.KeepAlive)*time.Second,
		tlsConfig,
	)

	producer, err := newProducer(
		kconf,
		brokers,
		c.Events,
		c.Items,
	)
	if err != nil {
		return nil, errs.Wrap(err, "failed to create new kafka producer")
	}

	return producer, nil
}


func getTLSConf(url, caFile, certFile, keyFile string) (*tls.Config, error) {
	d := tlsconfig.Details{
		RawUri:      url,
		TlsCaFile:   caFile,
		TlsCertFile: certFile,
		TlsKeyFile:  keyFile,
	}

	tlsConfig, err := d.GetTLSConfig(false)
	if err != nil {
		return nil, errs.Wrap(err, "failed to create TLS config")
	}

	return tlsConfig, nil
}

// newProducer returns a new producer initialized
// and ready to produce messages to Kafka.
func newProducer(config *sarama.Config, brokers []string, eventsTopic, itemsTopic string) (*DefaultProducer, error) {
	p, err := sarama.NewAsyncProducer(brokers, config)
	if err != nil {
		return nil, errs.Wrap(err, "async producer init failed")
	}

	prod := &DefaultProducer{
		async:       p,
		eventsTopic: eventsTopic,
		itemsTopic:  itemsTopic,
		timeout:     3 * time.Second,
	}

	go prod.errorListener()

	return prod, nil
}


//nolint:revive // configuration requires a lot of parameters
func newConfig(
	username,
	password string,
	retries int,
	tlsAuth,
	enableTLS bool,
	timeout,
	keepAlive time.Duration,
	tlsConf *tls.Config,
) *sarama.Config {
	config := sarama.NewConfig()
	config.ClientID = clientID
	config.Net.KeepAlive = keepAlive
	config.Net.DialTimeout = timeout
	config.Net.ReadTimeout = timeout
	config.Net.WriteTimeout = timeout
	config.Producer.Retry.Max = retries
	config.Net.TLS.Enable = enableTLS
	config.Metadata.AllowAutoTopicCreation = false

	if username != "" {
		config.Net.SASL.Enable = true
		config.Net.SASL.User = username
		config.Net.SASL.Password = password
	}

	if tlsAuth {
		config.Net.TLS.Enable = tlsAuth
		config.Net.TLS.Config = tlsConf
	}

	return config
}

func (p *DefaultProducer) errorListener() {
	for perr := range p.async.Errors() {
		log.Errf(
			"kafka producer error: %s, for topic %s, with key %s", perr.Err.Error(), perr.Msg.Topic, perr.Msg.Key)
	}
}

func (p *DefaultProducer) produce(m *sarama.ProducerMessage) {
	ticker := time.NewTicker(p.timeout)
	defer ticker.Stop()

	select {
	case p.async.Input() <- m:
		log.Debugf("new message produced with id: %s", m.Key)
	case <-ticker.C:
		log.Warningf("message send timeout for id: %s", m.Key)
	}
}
