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
var programVersion string

var globalSocketCount int = 0

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

type SocketContext struct {
	relayPool      RelayPool
	relayPoolMutex *sync.Mutex
	db             *pgxpool.Pool
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

func channelValid(checkChannel string) bool {
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
	log.Infof("Web socket creation request invalid channel '%s'", checkChannel)
	return false
}

func nextSocketNum() int {
	globalSocketCount = globalSocketCount + 1
	return globalSocketCount
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
	viper.SetDefault("CORSOrigins", []string{"*"})
	viper.SetDefault("BasePath", "/")
	viper.SetDefault("Channels", []string{"*"})
	if programVersion == "" {
		programVersion = "latest"
	}
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
	// log.Infof("Serving HTTPS at %s/", formatBaseURL(fmt.Sprintf("http://%s:%d",
	// 	viper.GetString("HttpHost"), viper.GetInt("HttpsPort")), basePath))
	log.Infof("Channels available: %s", strings.Join(viper.GetStringSlice("Channels"), ", "))

	// Make a database connection pool
	dbPool, err := dbConnect()
	if err != nil {
		os.Exit(1)
	}

	socketCtx := SocketContext{
		relayPool:      make(RelayPool),
		relayPoolMutex: &sync.Mutex{},
		db:             dbPool,
	}
	// relayMapMutex := &sync.Mutex{}

	ctxValue := context.WithValue(
		context.Background(),
		"socketCtx", socketCtx)
	ctxCancel, cancel := context.WithCancel(ctxValue)

	// HTTP router setup
	trimBasePath := strings.TrimLeft(basePath, "/")
	r := mux.NewRouter().
		StrictSlash(true).
		PathPrefix("/" + trimBasePath).
		Subrouter()
	// Return front page
	r.Handle("/", http.HandlerFunc(requestIndexHTML))
	r.Handle("/index.html", http.HandlerFunc(requestIndexHTML))
	// Initiate websocket subscription
	r.Handle("/listen/{channel}", webSocketHandler(ctxCancel))

	// Prepare the HTTP server object
	// More "production friendly" timeouts
	// https://blog.simon-frey.eu/go-as-in-golang-standard-net-http-config-will-break-your-production/#You_should_at_least_do_this_The_easy_path
	s := &http.Server{
		ReadTimeout:  2 * time.Second,
		WriteTimeout: 2 * time.Second,
		Addr:         fmt.Sprintf("%s:%d", viper.GetString("HttpHost"), viper.GetInt("HttpPort")),
		Handler:      r,
	}

	// Start http service
	go func() {
		// ListenAndServe returns http.ErrServerClosed when the server receives
		// a call to Shutdown(). Other errors are unexpected.
		if err := s.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal(err)
		}
	}()

	// Wait here for interrupt signal
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt)
	<-sig
	// Shut down everything attached to this context before exit
	cancel()
	time.Sleep(100 * time.Millisecond)
}

// Goroutine to watch a PgSQL LISTEN/NOTIFY channel, and
// send the notification object to the broadcast relay
// when an event comes through
func listenForNotify(ctx context.Context, listenChannel string) {

	socketInfo := ctx.Value("socketCtx").(SocketContext)

	// read (and/or create) a broadcast relay for this listen channel name
	socketInfo.relayPoolMutex.Lock()
	relay := socketInfo.relayPool.GetRelay(listenChannel)
	socketInfo.relayPoolMutex.Unlock()

	// Draw a connection from the pool to use for listening
	conn, err := socketInfo.db.Acquire(ctx)
	if err != nil {
		log.Fatalf("Error acquiring connection: %s", err)
	}
	defer conn.Release()
	log.Infof("Listening to the '%s' database channel\n", listenChannel)

	// Send the LISTEN command to the connection
	listenSQL := fmt.Sprintf("LISTEN %s", listenChannel)
	_, err = conn.Exec(ctx, listenSQL)
	if err != nil {
		log.Fatalf("Error listening to '%s' channel: %s", listenChannel, err)
	}

	// Wait for notifications to come off the connection
	for {
		notification, err := conn.Conn().WaitForNotification(ctx)
		if err != nil {
			log.Debugf("WaitForNotification, %s", err)
			return
		}

		log.Debugf("NOTIFY received, channel '%s', payload '%s'",
			notification.Channel,
			notification.Payload)

		// Send the notification to all listeners connected to the relay
		(*relay).NotifyCtx(ctx, *notification)
	}
}

// Generator function to create http.Handler to the http.Server
// to run. Handler converts request to websocket, and sets up
// goroutine to listen for broadcast messages from the db notification
// goroutine. Websocket is held open by pinging client regularly.
func webSocketHandler(ctx context.Context) http.Handler {

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		wsChannel := mux.Vars(r)["channel"]
		log.Debugf("request to open channel '%s' received", wsChannel)

		socketInfo := ctx.Value("socketCtx").(SocketContext)

		// Check the channel name against the allow channel names list/patterns
		if !channelValid(wsChannel) {
			w.WriteHeader(403) // Forbidden
			errMsg := fmt.Sprintf("requested channel '%s' is not allowed", wsChannel)
			log.Debug(errMsg)
			w.Write([]byte(errMsg))
			return
		}

		// Keep a unique number for each new socket we create
		wsNumber := nextSocketNum()

		// Create the web socket
		ws, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			log.Warnf("web socket creation failed: %s", err)
			return
		}
		log.Debugf("created websocket %d for channel '%s'", wsNumber, wsChannel)
		wsMutex := sync.Mutex{}
		defer ws.Close()

		// Only start a new database listener for new channels.
		// All other sockets just hang off the first listeners
		// relay broadcasts.
		rm := socketInfo.relayPool
		newChannel := rm.HasChannel(wsChannel)
		if !newChannel {
			go listenForNotify(ctx, wsChannel)
		}
		socketInfo.relayPoolMutex.Lock()
		relay := rm.GetRelay(wsChannel)
		socketInfo.relayPoolMutex.Unlock()

		// Listen to the broadcasts for this channel
		relayListener := (*relay).Listener(1)

		// Goroutine to monitor the relay listener and send messages
		// to the web socket client when new notifications arrive
		ctxWsCtx, wsCancel := context.WithCancel(ctx)
		go func(i int, lstnr *broadcast.Listener[pgconn.Notification], lstnrCtx context.Context) {
			defer lstnr.Close()
			// var n pgconn.Notification
			for {
				select {
				case n := <-lstnr.Ch():
					log.Debugf("sending notification to web socket %d: %s", i, n.Payload)
					bPayload := []byte(n.Payload)
					wsMutex.Lock()
					if err := ws.WriteMessage(websocket.TextMessage, bPayload); err != nil {
						log.Debugf("web socket %d closed connection", i)
						return
					}
					wsMutex.Unlock()
				case <-lstnrCtx.Done():
					log.Debugf("shutting down listener for web socket %d", i)
					return
				}
			}
		}(wsNumber, relayListener, ctxWsCtx)

		// Keep this web socket open as long as the client keeps
		// its side open
		for {
			select {
			case <-time.After(2 * time.Second):
				wsMutex.Lock()
				wserr := ws.WriteMessage(websocket.PingMessage, []byte("ping"))
				wsMutex.Unlock()
				// When socket no longer accepts writes, end this function,
				// which will do the defered close of the socket and listener
				if wserr != nil {
					log.Infof("Closing idle web socket %d", wsNumber)
					ws.Close()
					wsCancel()
					return
				}
			case <-ctx.Done():
				log.Debugf("webSocketHandler, closing websocket %d", wsNumber)
				ws.Close()
				wsCancel()
				return
			}
		}
		// h.ServeHTTP(w, r)
	})
}

func requestIndexHTML(w http.ResponseWriter, r *http.Request) {
	log.WithFields(log.Fields{
		"event": "request",
		"topic": "root",
	}).Trace("requestIndexHTML")

	type IndexFields struct {
		BaseURL  string
		Channels string
	}

	indexFields := IndexFields{
		BaseURL:  serverWsBase(r),
		Channels: strings.Join(viper.GetStringSlice("Channels"), ", "),
	}

	tmpl, err := template.ParseFiles(fmt.Sprintf("%s/index.html", viper.GetString("AssetsPath")))
	if err != nil {
		return
	}
	tmpl.Execute(w, indexFields)
	return
}
