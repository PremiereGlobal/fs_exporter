package main // import "github.com/PremiereGlobal/fs_exporter"

import (
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/PremiereGlobal/stim/pkg/stimlog"
	sets "github.com/deckarep/golang-set"
	"github.com/fiorix/go-eventsocket/eventsocket"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

func main() {
	slog := stimlog.GetLogger()
	config := viper.New()
	config.SetEnvPrefix("fse")
	config.AutomaticEnv()
	config.SetEnvKeyReplacer(strings.NewReplacer("-", "_"))

	var cmd = &cobra.Command{
		Use:   "fs_exporter",
		Short: "launch freeswitch exporter service",
		Long:  "launch freeswitch exporter service",
		Run: func(cmd *cobra.Command, args []string) {

			var ll stimlog.Level
			switch strings.ToLower(config.GetString("loglevel")) {
			case "info":
				ll = stimlog.InfoLevel
			case "warn":
				ll = stimlog.WarnLevel
			case "debug":
				ll = stimlog.DebugLevel
			case "trace":
				ll = stimlog.TraceLevel
			}
			slog.SetLevel(ll)

			stats := NewStats(slog, ll, config.GetString("host"), config.GetString("port"), config.GetString("password"))
			if !config.GetBool("disable-channels-total") {
				stats.StartChannelsTotal()
			}
			if !config.GetBool("disable-channels-current") {
				stats.StartChannelsCurrent()
			}
			if config.GetBool("enable-events-total") {
				stats.StartEventsTotal()
			}

			slog.Info("Metrics listening on port: {}", config.GetString("metricsPort"))

			http.Handle("/metrics", promhttp.Handler())
			time.Sleep(1) //Let some stats run before we start
			go http.ListenAndServe(fmt.Sprintf(":%s", config.GetString("metricsPort")), nil)
			for {
				time.Sleep(time.Minute * 5)
			}
		},
	}

	cmd.PersistentFlags().String("host", "127.0.0.1", "host to connect too")
	config.BindPFlag("host", cmd.PersistentFlags().Lookup("host"))
	cmd.PersistentFlags().String("port", "8021", "port to connect too")
	config.BindPFlag("port", cmd.PersistentFlags().Lookup("port"))
	cmd.PersistentFlags().String("password", "ClueCon", "event socket password")
	config.BindPFlag("password", cmd.PersistentFlags().Lookup("password"))
	cmd.PersistentFlags().String("metricsPort", "9143", "event socket password")
	config.BindPFlag("metricsPort", cmd.PersistentFlags().Lookup("metricsPort"))
	cmd.PersistentFlags().String("loglevel", "info", "level to show logs at (warn, info, debug, trace)")
	config.BindPFlag("loglevel", cmd.PersistentFlags().Lookup("loglevel"))

	cmd.PersistentFlags().Bool("disable-channels-total", false, "Enabled freeswitch_channels_total stats")
	config.BindPFlag("disable-channels-total", cmd.PersistentFlags().Lookup("disable-channels-total"))

	cmd.PersistentFlags().Bool("disable-channels-current", false, "Enabled freeswitch_channels_current stats")
	config.BindPFlag("disable-channels-current", cmd.PersistentFlags().Lookup("disable-channels-current"))

	cmd.PersistentFlags().Bool("enable-events-total", false, "Enables counting of all freeswitch events freeswitch_events_total stats")
	config.BindPFlag("enable-events-total", cmd.PersistentFlags().Lookup("enable-events-total"))

	err := cmd.Execute()
	if err != nil {
		slog.Fatal(err)
	}
}

type fsstats struct {
	host          string
	port          string
	password      string
	log           stimlog.StimLogger
	fscon         *eventsocket.Connection
	lastHeartBeat time.Time
	logLevel      stimlog.Level

	syncMap    sync.Map
	fsSendLock sync.Mutex

	latency_sec float64

	fs_init_commands sets.Set

	fs_channels_total   prometheus.CounterFunc
	fs_channels_current prometheus.Gauge
	fs_latency          prometheus.Histogram
	fs_alive            prometheus.Gauge
	fs_events_total     *prometheus.CounterVec
}

func NewStats(slog stimlog.StimLogger, ll stimlog.Level, host string, port string, password string) *fsstats {
	slog.Info("Connecting to: `{}:{}`", host, port)

	X := &fsstats{host: host, port: port, password: password, log: slog, latency_sec: -1, logLevel: ll}

	X.fs_latency = promauto.NewHistogram(prometheus.HistogramOpts{
		Name: "freeswitch_event_latency_seconds",
		Help: "EventSocket query latency",
	})
	X.fs_alive = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "freeswitch_alive",
		Help: "Freeswitch alive status, 0 if not connected 1 if connected",
	})
	X.fs_init_commands = sets.NewSet()
	X.fs_init_commands.Add("events json HEARTBEAT")
	go X.fseventLoop()
	go X.checkAlive()
	return X
}

func (fs *fsstats) StartEventsTotal() {
	totals := []string{"event"}
	cops := prometheus.CounterOpts{
		Name: "freeswitch_events_total",
		Help: "The number of total channels, as reported by freeswitch",
	}
	fs.fs_events_total = promauto.NewCounterVec(cops, totals)
	fs.fs_init_commands.Add("events json ALL")
	if fs.fscon != nil {
		fs.syncSend("events json ALL")
	}
}

func (fs *fsstats) StartChannelsCurrent() {
	fs.fs_channels_current = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "freeswitch_channels_current",
		Help: "The number of current channels, as reported by freeswitch",
	})
	go fs.getChannelsCurrent()
}

func (fs *fsstats) StartChannelsTotal() {
	fs.fs_channels_total = promauto.NewCounterFunc(prometheus.CounterOpts{
		Name: "freeswitch_channels_total",
		Help: "The number of total channels, as reported by freeswitch",
	}, fs.GetChannelsTotal)
	go fs.getChannelsTotal()
}

func (fs *fsstats) GetChannelsTotal() float64 {
	if i, ok := fs.syncMap.Load("total_channels"); ok {
		return i.(float64)
	}
	return -1
}

func (fs *fsstats) GetLatency() float64 {
	return fs.latency_sec
}

func (fs *fsstats) initConnection() error {
	fs.log.Debug("Connecting to:{}:{}", fs.host, fs.port)
	c, err := eventsocket.Dial(fmt.Sprintf("%s:%s", fs.host, fs.port), fs.password)
	if err != nil {
		return err
	}
	fs.fscon = c
	fs.log.Info("Connected To Freeswitch")
	fs.fs_init_commands.Each(func(i interface{}) bool {
		fs.syncSend(i.(string))
		return false
	})
	return nil
}

func (fs *fsstats) checkAlive() {
	for {
		if fs.fscon != nil && time.Since(fs.lastHeartBeat).Seconds() < 30 {
			fs.fs_alive.Set(1)
		} else {
			fs.fs_alive.Set(0)
		}
		time.Sleep(time.Second * 5)
	}
}

func (fs *fsstats) getChannelsTotal() {
	for {
		if fs.fscon != nil {
			ev, err := fs.syncSend("API status")
			if err != nil {
				fs.log.Warn("error getting status {}", err)
			} else {
				status_s := strings.Split(ev.Body, "\n")
				for _, sl := range status_s {
					if strings.HasSuffix(sl, "session(s) since startup") {
						tmp := strings.Split(sl, " ")
						v, err := strconv.ParseFloat(tmp[0], 64)
						if err != nil {
							fs.log.Warn("error parsing channels:\n{}\nerror: {}", sl, err)
							break
						}
						fs.syncMap.Store("total_channels", v)
						break
					}
				}
			}
		}
		time.Sleep(5 * time.Second)
	}
}

func (fs *fsstats) getChannelsCurrent() {
	for {
		if fs.fscon != nil {
			ev, err := fs.syncSend("API show channels count")
			if err != nil {
				fs.log.Warn("error sending show channels {}", err)
			} else {
				q := strings.Split(strings.TrimSpace(ev.Body), " ")
				v, err := strconv.ParseFloat(q[0], 64)
				if err != nil {
					fs.log.Warn("error parsing channel current:\n{}\nerror: {}", ev.String(), err)
				} else {
					fs.fs_channels_current.Set(v)
				}
			}
		}
		time.Sleep(5 * time.Second)
	}
}

func (fs *fsstats) syncSend(cmd string) (*eventsocket.Event, error) {
	fs.fsSendLock.Lock()
	fs.log.Trace("Send Locked")
	defer fs.log.Trace("Send Unlocked")
	defer fs.fsSendLock.Unlock()
	start := time.Now()
	fs.log.Debug("Sending FS command:\"{}\"", cmd)
	ev, err := fs.fscon.Send(cmd)
	if err == nil {
		sec_l := time.Since(start)
		fs.fs_latency.Observe(sec_l.Seconds())
		if fs.logLevel >= stimlog.DebugLevel {
			fs.log.Debug("syncSend, {}\nSent:\n\t{}\nRecv:\n{}\nError:\n\t{}", sec_l.Seconds(), cmd, PrettyPrint(ev), err)
		}
	}
	return ev, err
}

func (fs *fsstats) fseventLoop() {
	fs.log.Info("Starting Freeswitch eventLoop")
	for {
		if fs.fscon != nil {
			ev, err := fs.fscon.ReadEvent()
			if err != nil {
				fs.log.Warn(err)
				fs.fs_alive.Set(0)
				fs.fscon = nil
			} else {
				eventName := ev.Get("Event-Name")
				if fs.fs_events_total != nil {
					fs.fs_events_total.WithLabelValues(eventName).Inc()
				}
				if fs.logLevel >= stimlog.TraceLevel {
					fs.log.Trace("eventLoop:\nRecv:\n{}", PrettyPrint(ev))
				}
				switch eventName {
				case "HEARTBEAT":
					fs.lastHeartBeat = time.Now()
					break
				default:
					break
				}
			}
		} else {
			err := fs.initConnection()
			if err != nil {
				fs.log.Warn("Problems connecting to freeswitch:{}", err)
				time.Sleep(time.Second * 5)
			}
		}
	}
}

func PrettyPrint(r *eventsocket.Event) string {
	var sb strings.Builder
	var keys []string
	for k := range r.Header {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	sb.WriteString("\tHeaders:\n")
	for _, k := range keys {
		sb.WriteString(fmt.Sprintf("\t\t%s: %#v\n", k, r.Header[k]))
	}
	sb.WriteString("\tBody:\n")
	sb.WriteString(fmt.Sprintf("\t\t%#v\n", r.Body))
	return sb.String()
}
