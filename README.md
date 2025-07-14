# Kafka connector for Zabbix server

Kafka connector for Zabbix server is a lightweight server written in Go, designed to forward data from a Zabbix server to a Kafka broker.

Kafka connector currently supports forwarding *items* and *events* data.

## Requirements

- Go programming language version 1.20 or newer (required only for building the connector from source)

## Installation

1\. Download the Kafka connector [**source**](https://git.zabbix.com/projects/ZT/repos/kafka-connector).

2\. Transfer and extract the archive to the machine where you intend to build the connector.
Note that it can be built on any machine since the Kafka broker address is specified in the `Kafka.URL` configuration file option.

3\. Ensure you have the correct version of Go installed, enter the extracted directory, and then run `make build`.
This will create the `kafka-connector` executable in the extracted directory.

## Setup

The following setup instructions do not cover the configuration of a Kafka instance.
It is assumed that the Kafka instance is already configured.

1\. Modify the Kafka connector producer settings in `kafka_connector.conf` based on the configuration of your Kafka broker.

Note that Kafka connector **will not** create endpoints for Kafka topics if they do not exist in Kafka broker.

Kafka connector can also be executed with the default `kafka_connector.conf` configuration file.
However, by default, it will only accept connections from a Zabbix server running on `localhost`, and the endpoints of Kafka topics will be pre-defined.

2\. Configure a connector in Zabbix as described on the [**Streaming to external systems**](https://www.zabbix.com/documentation/7.0/en/manual/config/export/streaming#configuration) page in Zabbix Manual.
When specifying the receiver (Kafka connector) URLs in Zabbix frontend, ensure that they are specified as `http://<host>:<port>/api/v1/items` and `http://<host>:<port>/api/v1/events`.
Kafka connector listens for the data stream from Zabbix server at the paths `api/v1/items` and `api/v1/events`.

For encrypted connections, specify the relevant configuration options in the Kafka connector configuration file and Zabbix frontend, and replace `http` with `https` in the URLs.

3\. Execute the `kafka-connector` executable.

To ensure that the setup has been successful, you can check the Kafka connector log output (see `Connector.LogType` and `Connector.LogFile` configuration file options).
For debugging, increase the Kafka connector log level by adjusting the `Connector.LogLevel` configuration file option.

## Command-line options

As Kafka connector is a small utility, all configuration is done in the configuration file.
Therefore, only a few flags are available:

- `-h`, `--help`: Displays a help message.
- `-c`, `--config`: Displays the path to the configuration file (default: `./kafka_connector.conf`).

## Configuration options

### Kafka connector server settings

The following settings are used for the Kafka connector server.

#### Connector.Port

Kafka connector server port.

Default value: *80*

Example:

```conf
Connector.Port=8080
```

#### Connector.LogType

The log output type for Kafka connector.

Accepted values:
- *system* - *syslog*
- *file* - the file specified in the `Connector.LogFile` parameter
- *console* - standard output

Default value: *file*

Example:

```conf
Connector.LogType=file
```

#### Connector.LogFile

Log file location for Kafka connector.

Default value: */tmp/kafka-connector.log*

Example:

```conf
Connector.LogFile=/tmp/kafka-connector.log
```

#### Connector.LogFileSize

Maximum size (in MB) of the log file for Kafka connector.
Note that setting the value to `0` will disable log file rotation.

Accepted values range: *0-1024*

Default value: *1*

Example:

```conf
Connector.LogFileSize=1
```

#### Connector.LogLevel

Log level for Kafka connector.

Accepted values:
- *0* - basic information about starting and stopping of Zabbix processes
- *1* - critical information
- *2* - error information
- *3* - warnings
- *4* - for debugging (produces lots of information)
- *5* - extended debugging (produces even more information)

Default value: *3*

Example:

```conf
Connector.LogLevel=3
```

#### Connector.BearerToken

Authorization token for incoming connections from Zabbix server.

Example:

```conf
Connector.BearerToken=token
```

#### Connector.AllowedIP

List of comma-delimited IP addresses, optionally in CIDR notation, or DNS names.
Incoming connections will be accepted only from these hosts.
If IPv6 support is enabled, then `127.0.0.1`, `::127.0.0.1`, `::ffff:127.0.0.1` will be treated equally, and `::/0` will allow any IPv4 or IPv6 address.
`0.0.0.0/0` can also be used to allow any IPv4 address.

Default value: *127.0.0.1,::1*

Example:

```conf
Connector.AllowedIP=127.0.0.1,::1
```

#### Connector.EnableTLS

Enable TLS check for incoming requests.

Accepted values:
- *true*
- *false*

Default value: *false*

Example:

```conf
Connector.EnableTLS=true
```

#### Connector.CertFile

The full pathname to the certificate file, used for TLS verification of incoming requests.

Example:

```conf
Connector.CertFile=/path/to/file/cert.pem
```

#### Connector.KeyFile

The full pathname to the key file, used for TLS verification of incoming requests.

Example:

```conf
Connector.KeyFile=/path/to/file/key.pem
```

#### Connector.Timeout

Timeout (in seconds) for requests to Kafka broker.

Accepted values range: *1-30*

Default value: *3*

Example:

```conf
Connector.Timeout=15
```

### Kafka connector producer settings

The following settings are used for the Kafka connector producer.

#### Kafka.Brokers

Comma-separated list of Kafka brokers in the format host:port.

Default value: *localhost:9092*

Example:

```conf
Kafka.Brokers=broker2:9092,broker1:9092
```


#### Kafka.Events

The endpoint of the Kafka topic for events.

Default value: *events*

Example:

```conf
Kafka.Events=events
```

#### Kafka.Items

The endpoint of the Kafka topic for items.

Default value: *items*

Example:

```conf
Kafka.Items=items
```

#### Kafka.Retry

Kafka producer retry amount on failed request.

Default value: *0*

Example:

```conf
Kafka.Retry=4
```

#### Kafka.Timeout

Timeout (in seconds) for Kafka producer requests.

Default value: *1*

Example:

```conf
Kafka.Timeout=30
```

#### Kafka.KeepAlive

The duration (in seconds) for which to keep the Kafka producer connection alive.

Accepted values range: *60-300*

Default value: *300*

Example:

```conf
Kafka.KeepAlive=150
```

#### Kafka.Username

SASL authorization username.
If provided, enables SASL authorization.

Example:

```conf
Kafka.Username=zabbix
```

#### Kafka.Password

SASL authorization password.

Example:

```conf
Kafka.Password=zabbix
```

#### Kafka.EnableTLS

Enables Kafka TLS authorization.
Enables TLS connection when connecting to Kafka broker.

Accepted values:
- *true*
- *false*

Default value: *false*

Example:

```conf
Kafka.EnableTLS=true
```

#### Kafka.TLSAuth

Enables Kafka TLS authentication.
Enables providing TLS configuration (CA, certificate, and key) when connecting to Kafka broker.

Accepted values:
- *true*
- *false*

Default value: *false*

Example:

```conf
Kafka.TLSAuth=true
```

#### Kafka.CaFile

The full pathname to the CA file, used for TLS verification of outgoing requests.

Example:

```conf
Kafka.CaFile=/path/to/ca.pem
```

#### Kafka.ClientCertFile

The full pathname to the client certificate file, used for TLS verification of outgoing requests.

Example:

```conf
Kafka.ClientCertFile=/path/to/cert.pem
```

#### Kafka.ClientKeyFile

The full pathname to the client key file, used for TLS verification of outgoing requests

Example:

```conf
Kafka.ClientKeyFile=/path/to/key.pem
```

## Troubleshooting

For more information about Zabbix products, see [Zabbix documentation](https://www.zabbix.com/documentation/current/en/manual).

## Contributing

Noticed a bug or have an idea for improvement?
Feel free to open an issue or a feature request in [Zabbix support system](https://support.zabbix.com/secure/Dashboard.jspa).
