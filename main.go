package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"github.com/edgeware/mp4ff/mp4"
	twitterscraper "github.com/n0madic/twitter-scraper"
	"golang.org/x/term"
	"io"
	"io/fs"
	"log"
	"math"
	"net/http"
	"net/url"
	"os"
	"path"
	"strconv"
	"time"
)

var (
	output      *string
	decremental *bool
	incremental *bool
	since       *string
	until       *string
	limit       *int

	scraper *twitterscraper.Scraper
	client  *http.Client
)

func main() {
	output = flag.String("output", "", "path to output directory")
	decremental = flag.Bool("decremental", false, "search tweets decrementally")
	incremental = flag.Bool("incremental", false, "search tweets incrementally")
	since = flag.String("since", "", "filter tweets since this date")
	until = flag.String("until", "", "filter tweets until this date")
	limit = flag.Int("limit", math.MaxInt32, "limit number of tweets to search")
	flag.Parse()

	if *decremental && *incremental {
		log.Fatal("both --decremental and --incremental is specified")
	}

	scraper = twitterscraper.
		New().
		WithReplies(true).
		SetSearchMode(twitterscraper.SearchLatest)
	client = &http.Client{}

	if err := login(); err != nil {
		log.Fatal(err)
	}

	baseDir := *output
	for _, user := range flag.Args() {
		*output = path.Join(baseDir, user)
		if err := os.MkdirAll(*output, 0777); err != nil {
			log.Fatal(err)
		}

		if err := processUser(user); err != nil {
			log.Fatal(err)
		}
	}
}

func login() error {
	confPath := path.Join(GetConfigDir(), "twget")
	err := os.MkdirAll(confPath, 0700)
	if err != nil {
		return err
	}

	confPath = path.Join(confPath, "cookies.json")
	if _, err := os.Stat(confPath); errors.Is(err, fs.ErrNotExist) {
		return askPass(confPath)
	} else {
		f, err := os.Open(confPath)
		if err != nil {
			return err
		}
		defer f.Close()

		var cookies []*http.Cookie
		err = json.NewDecoder(f).Decode(&cookies)
		if err != nil {
			return err
		}
		scraper.SetCookies(cookies)
	}
	if !scraper.IsLoggedIn() {
		return askPass(confPath)
	}

	return nil
}

func askPass(confPath string) error {
	fmt.Print("username: ")
	var user string
	if _, err := fmt.Scanln(&user); err != nil {
		return err
	}

	fmt.Print("password: ")
	pass, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Println()
	if err != nil {
		return err
	}

	err = scraper.Login(user, string(pass))
	if err != nil {
		return err
	}
	if !scraper.IsLoggedIn() {
		return fmt.Errorf("failed to login")
	}

	f, err := os.Create(confPath)
	if err != nil {
		return err
	}
	defer f.Close()

	err = json.NewEncoder(f).Encode(scraper.GetCookies())
	if err != nil {
		log.Printf("failed to save cookie: %v", err)
	}

	return nil
}

func processUser(user string) error {
	q := make(map[string]string)
	q["from"] = user
	q["filter"] = "media"

	if *decremental || *incremental {
		if *decremental {
			t, err := findOldest()
			if err != nil {
				return err
			}

			if t != nil {
				y, m, d := t.Date()
				q["until"] = time.Date(y, m, d, 0, 0, 0, 0, t.Location()).
					AddDate(0, 0, 1).
					Format(time.DateOnly)
			}
		} else {
			t, err := findNewest()
			if err != nil {
				return err
			}

			if t != nil {
				q["since"] = t.Format(time.DateOnly)
			}
		}
	} else {
		q["since"] = *since
		q["until"] = *until
	}

	query := ""
	for key, value := range q {
		if value != "" {
			query += key + ":" + value + " "
		}
	}

	for tweet := range scraper.SearchTweets(context.TODO(), query, *limit) {
		if tweet.Error != nil {
			return tweet.Error
		}

		if tweet.IsRetweet {
			continue
		}

		for _, photo := range tweet.Photos {
			m := newMedia(photo.ID, photo.URL)
			name, err := m.processPhoto()
			if err != nil {
				return err
			}
			if name != nil {
				err = m.fixTimestamp(*name)
				if err != nil {
					return err
				}
			}
		}
		for _, video := range tweet.Videos {
			m := newMedia(video.ID, video.URL)
			name, err := m.processVideo()
			if err != nil {
				return err
			}
			if name != nil {
				err = m.fixTimestamp(*name)
				if err != nil {
					return err
				}
			}
		}
	}

	return nil
}

func findOldest() (*time.Time, error) {
	ee, err := os.ReadDir(*output)
	if err != nil {
		return nil, err
	}

	var oldest *time.Time
	for _, e := range ee {
		if e.IsDir() {
			continue
		}

		i, err := e.Info()
		if err != nil {
			return nil, err
		}

		t := i.ModTime()
		if oldest == nil || t.Before(*oldest) {
			oldest = &t
		}
	}

	return oldest, nil
}

func findNewest() (*time.Time, error) {
	ee, err := os.ReadDir(*output)
	if err != nil {
		return nil, err
	}

	var newest *time.Time
	for _, e := range ee {
		if e.IsDir() {
			continue
		}

		i, err := e.Info()
		if err != nil {
			return nil, err
		}

		t := i.ModTime()
		if newest == nil || t.After(*newest) {
			newest = &t
		}
	}

	return newest, nil
}

type Media struct {
	id  string
	url *url.URL
	ts  time.Time
}

func newMedia(id, rawURL string) *Media {
	u, _ := url.Parse(rawURL)
	n, _ := strconv.ParseInt(id, 10, 64)

	return &Media{
		id:  id,
		url: u,
		ts:  time.UnixMilli(n>>22 + 1288834974657),
	}
}

func (m *Media) processPhoto() (*string, error) {
	q := m.url.Query()
	q.Set("name", "orig")
	m.url.RawQuery = q.Encode()

	return m.download()
}

var mvhdEpoch = time.Date(1904, 1, 1, 0, 0, 0, 0, time.UTC)

func (m *Media) processVideo() (*string, error) {
	name, err := m.download()
	if name != nil {
		f, err := mp4.ReadMP4File(*name)
		if err != nil {
			return nil, err
		}

		ts := uint64(m.ts.Sub(mvhdEpoch).Seconds())
		f.Moov.Mvhd.CreationTime = ts
		f.Moov.Mvhd.ModificationTime = ts

		err = mp4.WriteToFile(f, *name)
		if err != nil {
			return nil, err
		}
	}

	return name, err
}

func (m *Media) download() (*string, error) {
	name := path.Join(*output, m.id+path.Ext(m.url.Path))
	if _, err := os.Stat(name); err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			return nil, err
		}
	} else {
		log.Printf("`%s` already exists", name)
		return nil, nil
	}

	log.Printf("downloading `%s`", name)

	resp, err := client.Get(m.url.String())
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	out, err := os.Create(name)
	if err != nil {
		return nil, err
	}
	defer out.Close()

	if _, err = io.Copy(out, resp.Body); err != nil {
		return nil, err
	}

	return &name, err
}

func (m *Media) fixTimestamp(name string) error {
	return os.Chtimes(name, m.ts, m.ts)
}
