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

package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"git.zabbix.com/ZT/kafka-connector/kafka"
	"git.zabbix.com/ZT/kafka-connector/server"
	"git.zabbix.com/ap/plugin-support/conf"
	"git.zabbix.com/ap/plugin-support/errs"
	"git.zabbix.com/ap/plugin-support/log"
	"git.zabbix.com/ap/plugin-support/zbxflag"
	"git.zabbix.com/ap/plugin-support/zbxnet"
)

const usageMessageFormat = //
`Usage of Zabbix agent 2:
  %[1]s [-c config-file]
  %[1]s -h

A Zabbix kafka producer for forwarding item and event data to a kafka broker.

Options:
%[2]s

Example: kafka-connector -c %[3]s

Report bugs to: <https://support.zabbix.com>
Zabbix home page: <https://www.zabbix.com>
Documentation: <https://www.zabbix.com/documentation>
`

type serverConf struct {
	Port        string `conf:"default=80"`
	LogType     string `conf:"default=file"`
	LogFile     string `conf:"default=/tmp/kafka-connector.log"`
	LogFileSize int    `conf:"range=0:1024,default=1"`
	BearerToken string `conf:"optional"`
	AllowedIP   string
	CertFile    string `conf:"optional"`
	KeyFile     string `conf:"optional"`
	LogLevel    int    `conf:"range=0:5,default=3"`
	EnableTLS   bool   `conf:"default=false"`
	Timeout     int    `conf:"range=1:30,default=3"`
}

type configuration struct {
	Kafka     kafka.Configuration `conf:"optional"`
	Connector serverConf          `conf:"optional"`
}

type arguments struct {
	configuration string
	help          bool
}

//nolint:revive // cognitive complexity of 8 is still quite readable and straightforward for main func
func main() {
	fs := flag.NewFlagSet("", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	args, err := getFlags(fs)
	if err != nil {
		fatalExit("failed to parse flags", err)
	}

	if args.help {
		fs.Usage()
		os.Exit(0)
	}

	var c configuration

	err = conf.Load(args.configuration, &c)
	if err != nil {
		fatalExit("failed to load the configuration", err)
	}

	err = initLogger(
		c.Connector.LogType,
		c.Connector.LogFile,
		c.Connector.LogLevel,
		c.Connector.LogFileSize,
	)
	if err != nil {
		fatalExit("failed to initialize the logger", err)
	}

	p, err := kafka.NewProducer(&c.Kafka)
	if err != nil {
		fatalExit("failed to initialize kafka producer", err)
	}

	allowedIPs, err := zbxnet.GetAllowedPeers(c.Connector.AllowedIP)
	if err != nil {
		fatalExit("failed to initialize allowed ip", err)
	}

	router := server.NewRouter(p, c.Connector.BearerToken, allowedIPs)

	s := server.ServerInit(c.Connector.Port, router, c.Connector.Timeout)

	log.Infof("Starting server")

	errors := make(chan error)

	go server.Run(s, c.Connector.CertFile, c.Connector.KeyFile, c.Connector.EnableTLS, errors)

	err = waitExit(errors)
	if err != nil {
		fatalExit("server failed", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)

	defer cancel()

	log.Infof("shutting down the server")

	err = s.Shutdown(ctx)
	if err != nil {
		log.Errf("failed to shutdown the server, %s", err.Error())
	}

	log.Debugf("shutting down the kafka producer")

	err = p.Close()
	if err != nil {
		log.Errf("failed to close Kafka producer, %s", err.Error())
	}

	log.Infof("Server shut down, good bye!")
}

func waitExit(errsChan <-chan error) error {
	sigs := createSigsChan()

	select {
	case <-sigs:
		log.Debugf("Received shutdown signal")

		return nil
	case err := <-errsChan:
		return err
	}
}

func createSigsChan() chan os.Signal {
	sigs := make(chan os.Signal, 1)

	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)

	return sigs
}

func initLogger(logType, logFile string, debugLevel, logFileSize int) error {
	err := log.Open(
		getLogType(logType),
		getLogLevel(debugLevel),
		logFile,
		logFileSize,
	)
	if err != nil {
		return errs.Wrap(err, "can not open logger")
	}

	return nil
}

func getLogType(logType string) int {
	switch logType {
	case "system":
		return log.System
	case "console":
		return log.Console
	case "file":
		return log.File
	}

	return log.Undefined
}

func getLogLevel(debugLevel int) int {
	if debugLevel < 0 || debugLevel > 5 {
		return log.Info
	}

	return debugLevel
}

func getFlags(fs *flag.FlagSet) (*arguments, error) {
	var (
		confDefault = "./kafka_connector.conf"
		a           = &arguments{}
	)

	f := zbxflag.Flags{
		&zbxflag.BoolFlag{
			Flag: zbxflag.Flag{
				Name:        "help",
				Shorthand:   "h",
				Description: "Display help message",
			},
			Default: false,
			Dest:    &a.help,
		},
		&zbxflag.StringFlag{
			Flag: zbxflag.Flag{
				Name:      "config",
				Shorthand: "c",
				Description: fmt.Sprintf(
					"Path to the configuration file (default: %q)", confDefault,
				),
			},
			Default: confDefault,
			Dest:    &a.configuration,
		},
	}

	f.Register(fs)
	fs.Usage = func() {
		fmt.Fprintf(
			os.Stdout,
			usageMessageFormat,
			filepath.Base(os.Args[0]),
			f.Usage(),
			confDefault,
		)
	}

	err := fs.Parse(os.Args[1:])
	if err != nil {
		return nil, errs.Wrap(err, "failed to parse flags")
	}

	return a, nil
}

//nolint:revive //fatalExit is called everywhere where os.Exit(1) is needed.
func fatalExit(message string, err error) {
	fmt.Fprintf(
		os.Stderr,
		"kafka-connector [%d]: ERROR: %s\n",
		os.Getpid(),
		fmt.Sprintf("%s: %s", message, err.Error()),
	)

	os.Exit(1)
}
