package main

import (
	"flag"
	"log"
	"os"
	"regexp"
	"strings"
	"time"

	"code.google.com/p/gcfg"
	geo "github.com/oschwald/geoip2-golang"
	goirc "github.com/thoj/go-ircevent"
)

type Config struct {
	Irc struct {
		Server               string
		Channel, ChannelPass string
		Nick, Nickpass       string
		TLS                  bool
		Debug                bool
	}
	Github struct {
		Debug                bool
		Token                string
		Repos                []string
		Channel, ChannelPass string
	}
	Untappd struct {
		Debug                  bool
		ClientID, ClientSecret string
		Users                  []string
		Channel, ChannelPass   string
	}
	Maxmind struct {
		DB        string
		available bool
		Reader    *geo.Reader
	}
}

var cfg Config

func main() {
	var (
		irc *goirc.Connection
		err error
	)
	var configFile = flag.String("c", "r2d2.cfg", "Load configuration from file")
	flag.Parse()
	_, err = os.Stat(*configFile)
	if err != nil {
		log.Fatal("%v", err)
		os.Exit(1)
	}
	err = gcfg.ReadFileInto(&cfg, *configFile)
	if err != nil {
		log.Fatal("Error in configuration file: %v", err)
		os.Exit(1)
	}
	irc = goirc.IRC(cfg.Irc.Nick, cfg.Irc.Nick)
	irc.UseTLS = cfg.Irc.TLS
	irc.VerboseCallbackHandler = cfg.Irc.Debug
	irc.Debug = cfg.Irc.Debug
	irc.Timeout = 300 * time.Second
	irc.PingFreq = 10 * time.Second
	irc.KeepAlive = 10 * time.Second
	err = irc.Connect(cfg.Irc.Server)
	if err != nil {
		log.Fatal("Connection to IRC server failed: %v", err)
		os.Exit(1)
	}

	// block while performing authentication
	handleAuth(irc)

	// we are identified, let's continue
	if cfg.Irc.ChannelPass != "" {
		// if a channel pass is used, craft a join command
		// of the form "&<channel>; <key>"
		irc.Join(cfg.Irc.Channel + " " + cfg.Irc.ChannelPass)
	} else {
		irc.Join(cfg.Irc.Channel)
	}
	if cfg.Irc.Debug {
		irc.Privmsg(cfg.Irc.Channel, "beep beedibeep dibeep")
	}
	go watchGithub(irc)
	go watchUntappd(irc)
	go fetchPageTitles(irc)
	initMaxmind()

	// add callback that captures messages sent to bot
	terminate := make(chan bool)
	irc.AddCallback("PRIVMSG", func(e *goirc.Event) {
		if cfg.Irc.Debug {
			log.Printf("%+v", e)
		}
		re := regexp.MustCompile("^" + cfg.Irc.Nick + ":(.+)$")
		if re.MatchString(e.Message()) {
			if cfg.Irc.Debug {
				log.Printf("message is for %q, processing %q", cfg.Irc.Nick, e.Message())
			}
			parsed := re.FindStringSubmatch(e.Message())
			if len(parsed) != 2 {
				log.Printf("Could not find a message body to work with. event=%+V", e)
				return
			}
			irchan := cfg.Irc.Channel
			if len(e.Arguments) > 0 {
				irchan = e.Arguments[0]
			}
			req := strings.Trim(parsed[1], " ")
			resp := handleRequest(req)
			log.Printf("responding with %q", resp)
			for i := 0; i <= len(resp); i += 300 {
				upper := 300
				if upper > len(resp[i:]) {
					upper = len(resp[i:])
				}
				// reply to the channel the request came from
				irc.Privmsgf(irchan, "%s: %s", e.Nick, resp[i:upper])
				log.Printf("channel: %q; caller: %q; msg: %q", e.Arguments[0], e.Nick, resp[i:upper])
			}
		}
	})
	<-terminate
	irc.Loop()
	irc.Disconnect()
}

func handleAuth(irc *goirc.Connection) {
	// place a callback on nickserv identification and wait until it is done
	if cfg.Irc.Nickpass != "" {
		identwaiter := make(chan bool)
		irc.AddCallback("NOTICE", func(e *goirc.Event) {
			re := regexp.MustCompile("NickServ IDENTIFY")
			if e.Nick == "NickServ" && re.MatchString(e.Message()) {
				irc.Privmsgf("NickServ", "IDENTIFY %s", cfg.Irc.Nickpass)
			}
			reaccepted := regexp.MustCompile("(?i)Password accepted")
			if e.Nick == "NickServ" && reaccepted.MatchString(e.Message()) {
				identwaiter <- true
			}
		})
		for {
			select {
			case <-identwaiter:
				goto identified
			case <-time.After(5 * time.Second):
				irc.Privmsgf("NickServ", "IDENTIFY %s", cfg.Irc.Nickpass)
			}
		}
	identified:
		irc.ClearCallback("NOTICE")
		close(identwaiter)
	}
	return
}

// handleRequest receives a request as a string and attempt to answer it by looking
// at the first word as a keyword.
func handleRequest(req string) string {
	log.Printf("handling request %q", req)
	command := strings.Split(req, " ")
	switch command[0] {
	case "fly":
		return "PPPPPPFFFFFfffffffffiiiiiiiiiuuuuuuuuuuuuuuuu....................."
	case "flip":
		return "(ﾉಥ益ಥ）ﾉ ┻━┻ " + strings.Join(command[1:], " ")
	case "github":
		if len(command) > 1 && command[1] == "repos" {
			return githubPrintReposList()
		}
		return "try 'help github'"
	case "help":
		if len(command) > 1 {
			return printHelpFor(command[1])
		}
		return "try 'help <command>', supported commands are: time, github, fly, flip, stardate, untappd and weather"
	case "ip":
		if len(command) > 1 {
			return geolocate(command[1])
		}
		return "try 'help ip'"
	case "time":
		if len(command) > 1 {
			return getTimeIn(command[1])
		}
		return getTimeIn("")
	case "stardate":
		return stardateCalc()
	case "weather":
		if len(command) < 2 {
			return weatherHelp
		}
		return getYahooForecast(strings.Join(command[1:], " "))
	case "untappd":
		if len(command) > 1 && command[1] == "users" {
			return untappdPrintUsers()
		}
		return "try 'help untappd'"
	default:
		return "I do not know how to answer this..."
	}
}

func printHelpFor(command string) string {
	switch command {
	case "github":
		return githubHelp
	case "time":
		return timeHelp
	case "ip":
		return geolocationHelp
	case "weather":
		return weatherHelp
	case "untappd":
		return untappdHelp
	default:
		return "there is no help for " + command
	}
}
