# fs_exporter
[![Build][Build-Status-Image]][Build-Status-Url]

https://travis-ci.org/PremiereGlobal/fs_exporter#

This is a prometheus exporter for freeswitch.

## Usage

fs_exporter needs to connect to freeswitch's event socket which requires an IP and password.
To set the IP/password use these options:

* host - the freeswitch host to connect too (defaults to localhost)
* port - the freeswitch event port to connect too (defaults to 8021)
* password - the freeswitch event socket password to use (defaults to ClueCon)

Other options are:

* metricsPort - the prometheus port to open for serving prometheus status_s
* loglevel - the level to set the logging to (warn, info, debug, trace)

Options for metrics are:

* disable-channels-total - disable polling the current freeswitch total channels.
* disable-channels-current - disable polling the current number of channels from freeswitch.
* enable-events-total - enable watching all events from freeswitch.  This defaults to disabled since it can cause higher load on certain boxes, enable it if useful.

[Build-Status-Url]: https://travis-ci.org/PremiereGlobal/fs_exporter
[Build-Status-Image]: https://travis-ci.org/PremiereGlobal/fs_exporter.svg?branch=master
