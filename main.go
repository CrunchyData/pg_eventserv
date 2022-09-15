package main

import (

	// Core
	"context"
	"fmt"
	"html/template"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"time"

	// Web Sockets Library
	"github.com/gorilla/websocket"

	// REST routing
	"github.com/gorilla/mux"

	// Pattern match channel names
	"github.com/komem3/glob"

	// Send multiple channel messages at once
	// used to send notificaitons to web sockts
	"github.com/teivah/broadcast"

	// Configuration utilities
	"github.com/pborman/getopt/v2"
	"github.com/spf13/viper"

	// Logging
	log "github.com/sirupsen/logrus"

	// PostgreSQL connection
	"github.com/jackc/pgconn"
	"github.com/jackc/pgx/v4/pgxpool"
)

var pool *pgxpool.Pool

// programName is the name string we use
const programName string = "pg_eventserv"

// programVersion is the version string we use
const programVersion string = "0.1"

// var programVersion string
var globalSocketCount int = 0

func nextSocketNum() int {
	globalSocketCount = globalSocketCount + 1
	return globalSocketCount
}

// globalDb is a global database connection pointer
var globalDb *pgxpool.Pool = nil

var upgrader = websocket.Upgrader{
	HandshakeTimeout: time.Second,
	ReadBufferSize:   1024,
	WriteBufferSize:  1024,
	Error: func(w http.ResponseWriter, r *http.Request, status int, reason error) {
		if _, errWrite := w.Write([]byte("websocket connection failed\n")); errWrite != nil {
			log.Fatal("unable to write web socket error to output")
		}
		return
	},
	// Temporarily disable origin checking on the websockets upgrader
	CheckOrigin:       func(r *http.Request) bool { return true },
	EnableCompression: false,
}

/**********************************************************************/

type BcastRelay *broadcast.Relay[pgconn.Notification]
type RelayPool map[string]BcastRelay

// get a relay from the map if possible, otherwise
// create a new one and store it in the map
func (rm RelayPool) GetRelay(channel string) (er BcastRelay) {
	if relay, ok := rm[channel]; ok {
		return relay
	}
	rm[channel] = broadcast.NewRelay[pgconn.Notification]()
	return rm[channel]
}

func (rm RelayPool) Close(channel string) {
	if relay, ok := rm[channel]; ok {
		(*relay).Close()
		delete(rm, channel)
	}
}

func (rm RelayPool) CloseAll() {
	for channel, relay := range rm {
		(*relay).Close()
		delete(rm, channel)
	}
}

func (rm RelayPool) HasChannel(channel string) bool {
	if _, ok := rm[channel]; ok {
		return true
	} else {
		return false
	}
}

/**********************************************************************/

func ChannelValid(checkChannel string) bool {
	validList := viper.GetStringSlice("Channels")
	for _, channelGlob := range validList {
		matcher, err := glob.Compile(channelGlob)
		if err != nil {
			log.Warnf("invalid channel pattern in Channels configuration: %s", channelGlob)
			return false
		}
		isValid := matcher.MatchString(checkChannel)
		if isValid {
			return true
		}
	}
	log.Infof("web socket creation request invalid channel '%s'", checkChannel)
	return false
}

/**********************************************************************/

func init() {
	viper.SetDefault("DbConnection", "sslmode=disable")
	viper.SetDefault("HttpHost", "0.0.0.0")
	viper.SetDefault("HttpPort", 7700)
	viper.SetDefault("HttpsPort", 7701)
	viper.SetDefault("TlsServerCertificateFile", "")
	viper.SetDefault("TlsServerPrivateKeyFile", "")
	viper.SetDefault("UrlBase", "")
	viper.SetDefault("Debug", false)
	viper.SetDefault("AssetsPath", "./assets")
	// 1d, 1h, 1m, 1s, see https://golang.org/pkg/time/#ParseDuration
	viper.SetDefault("DbPoolMaxConnLifeTime", "1h")
	viper.SetDefault("DbPoolMaxConns", 4)
	viper.SetDefault("DbTimeout", 10)
	viper.SetDefault("CORSOrigins", []string{"*"})
	viper.SetDefault("BasePath", "/")
	viper.SetDefault("Channels", []string{"*"})
}

func main() {

	flagDebugOn := getopt.BoolLong("debug", 'd', "log debugging information")
	flagConfigFile := getopt.StringLong("config", 'c', "", "full path to config file", "config.toml")
	flagHelpOn := getopt.BoolLong("help", 'h', "display help output")
	flagVersionOn := getopt.BoolLong("version", 'v', "display version number")
	getopt.Parse()

	if *flagHelpOn {
		getopt.PrintUsage(os.Stdout)
		os.Exit(1)
	}

	if *flagVersionOn {
		fmt.Printf("%s %s\n", programName, programVersion)
		os.Exit(0)
	}

	viper.AutomaticEnv()
	viper.SetEnvPrefix("es")

	// Commandline over-rides config file for debugging
	if *flagDebugOn {
		viper.Set("Debug", true)
		log.SetLevel(log.TraceLevel)
	}

	if *flagConfigFile != "" {
		viper.SetConfigFile(*flagConfigFile)
	} else {
		viper.SetConfigName(programName)
		viper.SetConfigType("toml")
		viper.AddConfigPath("./config")
		viper.AddConfigPath("/config")
		viper.AddConfigPath("/etc")
	}

	// Report our status
	log.Infof("%s %s", programName, programVersion)
	log.Info("Run with --help parameter for commandline options")

	// Read environment configuration first
	if dbURL := os.Getenv("DATABASE_URL"); dbURL != "" {
		viper.Set("DbConnection", dbURL)
		log.Info("Using database connection info from environment variable DATABASE_URL")
	}

	if err := viper.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); ok {
			log.Debugf("viper.ConfigFileNotFoundError: %s", err)
		} else {
			if _, ok := err.(viper.UnsupportedConfigError); ok {
				log.Debugf("viper.UnsupportedConfigError: %s", err)
			} else {
				log.Fatalf("Configuration file error: %s", err)
			}
		}
	} else {
		// Log location of filename we found...
		if cf := viper.ConfigFileUsed(); cf != "" {
			log.Infof("Using config file: %s", cf)
		} else {
			log.Info("Config file: none found, using defaults")
		}
	}

	basePath := viper.GetString("BasePath")
	log.Infof("Serving HTTP  at %s/", formatBaseURL(fmt.Sprintf("http://%s:%d",
		viper.GetString("HttpHost"), viper.GetInt("HttpPort")), basePath))
	log.Infof("Serving HTTPS at %s/", formatBaseURL(fmt.Sprintf("http://%s:%d",
		viper.GetString("HttpHost"), viper.GetInt("HttpsPort")), basePath))
	log.Infof("Channels available: %s", strings.Join(viper.GetStringSlice("Channels"), ", "))

	// made a database connection pool
	db, err := dbConnect()
	if err != nil {
		log.Fatal("database connection failed")
		os.Exit(1)
	}

	relayMap := make(RelayPool)
	relayMapMutex := &sync.Mutex{}
	wsHandlerFunc := webSocketHandler(relayMap, relayMapMutex, db)

	r := getRouter(wsHandlerFunc)

	// prepare the HTTP server object
	// more "production friendly" timeouts
	// https://blog.simon-frey.eu/go-as-in-golang-standard-net-http-config-will-break-your-production/#You_should_at_least_do_this_The_easy_path
	s := &http.Server{
		ReadTimeout:  2 * time.Second,
		WriteTimeout: 2 * time.Second,
		Addr:         fmt.Sprintf("%s:%d", viper.GetString("HttpHost"), viper.GetInt("HttpPort")),
		Handler:      r,
	}

	// start http service
	go func() {
		// ListenAndServe returns http.ErrServerClosed when the server receives
		// a call to Shutdown(). Other errors are unexpected.
		if err := s.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal(err)
		}
	}()

	// wait here for interrupt signal
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt)
	<-sig

}

// goroutine to watch a PgSQL LISTEN/NOTIFY channel, and
// send the notification object to the broadcast relay
// when an event comes through
func listenForNotify(db *pgxpool.Pool, listenChannel string, relay BcastRelay) {
	// draw a connection from the pool to use for listening
	conn, err := db.Acquire(context.Background())
	if err != nil {
		log.Fatalf("Error acquiring connection: %s", err)
	}
	defer conn.Release()
	log.Infof("Listening to the '%s' database channel\n", listenChannel)

	// send the LISTEN command to the connection
	listenSQL := fmt.Sprintf("LISTEN %s", listenChannel)
	_, err = conn.Exec(context.Background(), listenSQL)
	if err != nil {
		log.Fatalf("Error listening to '%s' channel: %s", listenChannel, err)
	}

	// wait for notifications to come off the connection
	for {
		notification, err := conn.Conn().WaitForNotification(context.Background())
		if err != nil {
			log.Warnf("WaitForNotification failed: %s", err)
			(*relay).Close()
		}

		log.Debugf("NOTIFY received, channel '%s', payload '%s'",
			notification.Channel,
			notification.Payload)

		// send the notification to all listeners connected to the relay
		(*relay).NotifyCtx(context.Background(), *notification)
	}
}

// generator function to create http.Handler to the http.Server
// to run. Handler converts request to websocket, and sets up
// goroutine to listen for broadcast messages from the db notification
// goroutine. Websocket is held open by pinging client regularly.
func webSocketHandler(rm RelayPool, rmMutex *sync.Mutex, db *pgxpool.Pool) http.Handler {

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		wsChannel := mux.Vars(r)["channel"]
		log.Debugf("request to open channel '%s' received", wsChannel)

		if !ChannelValid(wsChannel) {
			w.WriteHeader(403) // forbidden
			errMsg := fmt.Sprintf("requested channel '%s' is not allowed", wsChannel)
			log.Debug(errMsg)
			w.Write([]byte(errMsg))
			return
		}

		// keep a unique number for each new socket we create
		wsNumber := nextSocketNum()

		// create the web socket
		ws, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			log.Warnf("web socket creation failed: %s", err)
			return
		}
		log.Debugf("created websocket %d for channel '%s'", wsNumber, wsChannel)
		defer ws.Close()

		// make a broadcast listener for this socket
		rmMutex.Lock()
		newChannel := rm.HasChannel(wsChannel)
		relay := rm.GetRelay(wsChannel)
		rmMutex.Unlock()
		// Only start a new database listener for new channels
		// All other sockets just hang off the first listeners
		// relay broadcasts
		if !newChannel {
			go listenForNotify(db, wsChannel, relay)
		}
		lst := (*relay).Listener(1)
		defer lst.Close()

		// Goroutine to monitor the relay listener and send messages
		// to the web socket client when new notifications arrive
		go func() {
			for n := range lst.Ch() { // Ranges over notifications
				log.Debugf("sending notification to web socket %d: %s", wsNumber, n.Payload)
				bPayload := []byte(n.Payload)
				if err := ws.WriteMessage(websocket.TextMessage, bPayload); err != nil {
					log.Debugf("web socket %d closed connection", wsNumber)
					return
				}
			}
		}()

		// Keep this web socket open as long as the client keeps
		// its side open
		for {
			wserr := ws.WriteMessage(websocket.PingMessage, []byte("ping"))
			// When socket no longer accepts writes, end this function,
			// which will do the defered close of the socket and listener
			if wserr != nil {
				log.Infof("closing web socket %d after write failure", wsNumber)
				return
			}
			// Only ping the client every few seconds
			time.Sleep(time.Second * 2)
		}
		// h.ServeHTTP(w, r)
	})
}

func getRouter(wsHandler http.Handler) *mux.Router {
	// creates a new instance of a mux router
	r := mux.NewRouter().
		StrictSlash(true).
		PathPrefix(
			"/" +
				strings.TrimLeft(viper.GetString("BasePath"), "/"),
		).
		Subrouter()

	// Front page
	r.Handle("/", http.HandlerFunc(requestIndexHTML))
	r.Handle("/index.html", http.HandlerFunc(requestIndexHTML))
	// Channel websocket subscription
	r.Handle("/listen/{channel}", wsHandler)
	return r
}

func requestIndexHTML(w http.ResponseWriter, r *http.Request) {
	log.WithFields(log.Fields{
		"event": "request",
		"topic": "root",
	}).Trace("requestIndexHTML")

	type IndexFields struct {
		BaseURL string
	}

	indexFields := IndexFields{serverWsBase(r)}
	tmpl, err := template.ParseFiles(fmt.Sprintf("%s/index.html", viper.GetString("AssetsPath")))
	if err != nil {
		return
	}
	tmpl.Execute(w, indexFields)
	return
}
