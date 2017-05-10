package main

import (
	"crypto/tls"
	"flag"
	"fmt"
	"github.com/BurntSushi/toml"
	"github.com/ryanuber/go-glob"
	"github.com/thoj/go-ircevent"
	"os"
	"regexp"
	"strings"
)

type Channel struct {
	Name                string
	ManageWhiteList     bool
	WhiteListSelfJoin   bool
	WhiteListNicks      []string
	WhiteListConnection []string
	CleanWhiteList      bool
	MinOperatingMode    bool
}

func (c *Channel) protectedNick(nick string) bool {
	for _, v := range c.WhiteListNicks {
		if nick == v {
			return true
		}
	}
	return false
}

func (c *Channel) protectedConn(conn string) bool {
	for _, v := range c.WhiteListConnection {
		if glob.Glob(v, conn) {
			return true
		}
	}
	return false
}

type Config struct {
	Nick           string
	NickPassword   string
	User           string
	UseTLS         bool
	TLSSkipVerify  bool
	Server         string
	ServerPassword string
	Channel        []Channel
	Admins         []string
}

func (cfg *Config) findChannel(name string) *Channel {
	for k, v := range cfg.Channel {
		if name == v.Name {
			return &cfg.Channel[k]
		}
	}
	return nil
}

func (cfg *Config) adminUser(source string) bool {
	for _, v := range cfg.Admins {
		if glob.Glob(v, source) {
			return true
		}
	}
	return false
}

func (cfg *Config) write(filename string) error {
	f, err := os.Create(filename)
	if err != nil {
		return err
	}
	defer f.Close()

	err = toml.NewEncoder(f).Encode(cfg)
	return err
}

func (cfg *Config) addWhiteList(c *Channel, nick string) error {
	// check to make sure the nick isn't in the list already
	for _, v := range c.WhiteListNicks {
		if v == nick {
			return nil
		}
	}

	c.WhiteListNicks = append(c.WhiteListNicks, nick)
	return cfg.write(configFile)
}

func (cfg *Config) removeWhiteList(c *Channel, nick string) error {
	for k, v := range c.WhiteListNicks {
		if v == nick {
			c.WhiteListNicks = append(c.WhiteListNicks[:k], c.WhiteListNicks[k+1:]...)
			return cfg.write(configFile)
		}
	}
	return nil
}

func (cfg *Config) AdminREPL(ircobj *irc.Connection, e *irc.Event) {
	fmt.Println("ADMIN QUERY")
}

//func (s string) m(regex string) []string {
//	exp := regexp.MustCompile(regex)
//	cmd

var add_cmd = regexp.MustCompile(`^add\s+([a-zA-Z0-9_\-\\\[\]\{\}\^\` + "`" + `\|]+)\s*$`)
var remove_cmd = regexp.MustCompile(`^remove\s+([a-zA-Z0-9_\-\\\[\]\{\}\^\` + "`" + `\|]+)\s*$`)

func (cfg *Config) REPL(ircobj *irc.Connection, e *irc.Event, channel *Channel) {
	message := e.Arguments[1]
	message = regexp.MustCompile(`^\s*`+cfg.Nick+`[[:punct:]]*\s+`).ReplaceAllLiteralString(message, "")
	if cmd := add_cmd.FindStringSubmatch(message); cmd != nil {
		nick := cmd[1]
		fmt.Println("Add CMD:", cmd)
		if err := cfg.addWhiteList(channel, nick); err != nil {
			fmt.Println(err)
		}
		ircobj.Mode(channel.Name, "+e", nick+"!*@*")
	} else if cmd := remove_cmd.FindStringSubmatch(message); cmd != nil {
		fmt.Println("Remove CMD:", cmd)
		nick := cmd[1]
		if err := cfg.removeWhiteList(channel, nick); err != nil {
			fmt.Println(err)
		}
		ircobj.Mode(channel.Name, "-e", nick+"!*@*")
	} else {
		fmt.Println("MESSAGE:", message)
	}
}

var configFile string
var debug bool

func init() {
	flag.StringVar(&configFile, "config", "", "Path to config file")
	flag.BoolVar(&debug, "debug", false, "Enable or disable debugging")
}

func main() {
	flag.Parse()

	if configFile == "" {
		fmt.Println("A config file is required.")
		return
	}

	var conf Config
	if _, err := toml.DecodeFile(configFile, &conf); err != nil {
		fmt.Println(err)
		return
	}

	var channelOccupancy = make(map[*Channel]map[string]bool)
	for k, _ := range conf.Channel {
		channelOccupancy[&conf.Channel[k]] = make(map[string]bool)
	}
	// Initialize a ircobj
	ircobj := irc.IRC(conf.Nick, conf.User)
	//Set options
	if debug {
		ircobj.VerboseCallbackHandler = true
		ircobj.Debug = true
	}

	if conf.UseTLS {
		ircobj.UseTLS = true
	}
	if conf.TLSSkipVerify {
		ircobj.TLSConfig = &tls.Config{InsecureSkipVerify: true}
	}
	if conf.ServerPassword != "" {
		ircobj.Password = conf.ServerPassword
	}

	//Commands
	ircobj.AddCallback("001", func(e *irc.Event) {
		if conf.NickPassword != "" {
			ircobj.Privmsg("nickserv", "identify "+conf.NickPassword)
		}
		for _, v := range conf.Channel {
			ircobj.Join(v.Name)
		}
	})

	var nick_regex = regexp.MustCompile(`[@\+]?([a-zA-Z0-9_\-\\\[\]\{\}\^\` + "`" + `\|]+)\s*$`)
	ircobj.AddCallback("353", func(e *irc.Event) {
		channel := conf.findChannel(e.Arguments[2])
		if channel != nil {
			users := strings.Split(e.Arguments[3], " ")
			for _, v := range users {
				re := nick_regex.FindStringSubmatch(v)
				if len(re) > 0 {
					channelOccupancy[channel][re[1]] = true
				}
			}
		}
	})

	ircobj.AddCallback("366", func(e *irc.Event) {
		channel := conf.findChannel(e.Arguments[1])
		if channel != nil {
			if channel.MinOperatingMode {
				ircobj.Mode(channel.Name, "-o", conf.Nick)
			} else {
				ircobj.Privmsg("chanserv", "op "+channel.Name+" "+conf.Nick)
			}
		}
	})

	ircobj.AddCallback("JOIN", func(e *irc.Event) {
		channel := conf.findChannel(e.Arguments[0])
		if channel != nil {
			channelOccupancy[channel][e.Nick] = true

			if channel.ManageWhiteList {

				if channel.protectedNick(e.Nick) || channel.protectedConn(e.Host) {
					ircobj.Mode(channel.Name, "+e", e.Nick+"!*@*")
				} else {
					ircobj.Noticef(e.Nick,
						"Hello, please send a private message to <%s> if you would like to participate in the discussion. /msg %s hello",
						conf.Nick,
						conf.Nick)
				}
			}
		}
	})

	ircobj.AddCallback("KICK", func(e *irc.Event) {
		nick := e.Arguments[1]
		channel := conf.findChannel(e.Arguments[0])
		if channel != nil {
			delete(channelOccupancy[channel], nick)

			if channel.ManageWhiteList {

				if err := conf.removeWhiteList(channel, nick); err != nil {
					fmt.Println(err)
				}
				ircobj.Mode(channel.Name, "-e", nick+"!*@*")
			}
		}
	})

	ircobj.AddCallback("PART", func(e *irc.Event) {
		channel := conf.findChannel(e.Arguments[0])
		if channel != nil {
			delete(channelOccupancy[channel], e.Nick)

			if channel.ManageWhiteList && channel.CleanWhiteList {
				ircobj.Mode(channel.Name, "-e", e.Nick+"!*@*")
			}
		}
	})
	ircobj.AddCallback("QUIT", func(e *irc.Event) {
		for k, v := range conf.Channel {
			delete(channelOccupancy[&conf.Channel[k]], e.Nick)

			if v.ManageWhiteList && v.CleanWhiteList {
				ircobj.Mode(v.Name, "-e", e.Nick+"!*@*")
			}

		}
	})

	ircobj.AddCallback("NICK", func(e *irc.Event) {
		for k, v := range conf.Channel {
			if channelOccupancy[&conf.Channel[k]][e.Nick] {
				delete(channelOccupancy[&conf.Channel[k]], e.Nick)
				channelOccupancy[&conf.Channel[k]][e.Arguments[0]] = true
			}

			if v.protectedNick(e.Nick) || v.protectedConn(e.Host) {
				if err := conf.removeWhiteList(&conf.Channel[k], e.Nick); err != nil {
					fmt.Println(err)
				}

				if err := conf.addWhiteList(&conf.Channel[k], e.Arguments[0]); err != nil {
					fmt.Println(err)
				}
				ircobj.Mode(v.Name, "-e+e", e.Nick+"!*@*", e.Arguments[0]+"!*@*")

			} else if v.CleanWhiteList {
				ircobj.Mode(v.Name, "-e", e.Nick+"!*@*")
			}
		}
	})

	ircobj.AddCallback("PRIVMSG", func(e *irc.Event) {
		target := e.Arguments[0]
		message := e.Arguments[1]

		if target == conf.Nick {
			fmt.Println("DIRECT MESSAGE")
			for k, v := range conf.Channel {
				if v.ManageWhiteList &&
					v.WhiteListSelfJoin &&
					!v.protectedNick(e.Nick) &&
					channelOccupancy[&conf.Channel[k]][e.Nick] {
					if err := conf.addWhiteList(&conf.Channel[k], e.Nick); err != nil {
						fmt.Println(err)
					}
					ircobj.Mode(v.Name, "+e", e.Nick+"!*@*")
				}
			}

			if conf.adminUser(e.Source) {
				conf.AdminREPL(ircobj, e)
			}
		} else {
			channel := conf.findChannel(target)
			if channel != nil && strings.HasPrefix(message, conf.Nick) {
				// direct message
				conf.REPL(ircobj, e, channel)
			}
		}
	})

	err := ircobj.Connect(conf.Server)
	if err != nil {
		fmt.Printf("Err %s", err)
		return
	}

	ircobj.Loop()
}
