package main

import (
	"bytes"
	"context"
	"database/sql"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"regexp"
	"sort"
	"strings"
	"text/template"
	"time"

	"github.com/PuerkitoBio/goquery"
	comatproto "github.com/bluesky-social/indigo/api/atproto"
	"github.com/bluesky-social/indigo/api/bsky"
	lexutil "github.com/bluesky-social/indigo/lex/util"
	cliutil "github.com/bluesky-social/indigo/util/cliutil"
	"github.com/bluesky-social/indigo/xrpc"
	_ "github.com/lib/pq"
	"github.com/mmcdole/gofeed"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/pgdialect"
)

const name = "feed2bsky"

const version = "0.0.15"

var revision = "HEAD"

type Feed2Bsky struct {
	bun.BaseModel `bun:"table:feed2bsky,alias:f"`

	Feed      string    `bun:"feed,pk,notnull" json:"feed"`
	GUID      string    `bun:"guid,pk,notnull" json:"guid"`
	CreatedAt time.Time `bun:"created_at,notnull,default:current_timestamp" json:"created_at"`
}

type config struct {
	Host     string `json:"host"`
	Handle   string `json:"handle"`
	Password string `json:"password"`
}

type entry struct {
	start int64
	end   int64
	text  string
}

const (
	urlPattern = `https?://[-A-Za-z0-9+&@#\/%?=~_|!:,.;\(\)]+`
	tagPattern = `\B#\S+`
)

var (
	urlRe = regexp.MustCompile(urlPattern)
	tagRe = regexp.MustCompile(tagPattern)
)

func extractLinksBytes(text string) []entry {
	var result []entry
	matches := urlRe.FindAllStringSubmatchIndex(text, -1)
	for _, m := range matches {
		result = append(result, entry{
			text:  text[m[0]:m[1]],
			start: int64(len(text[0:m[0]])),
			end:   int64(len(text[0:m[1]]))},
		)
	}
	return result
}

func extractTagsBytes(text string) []entry {
	var result []entry
	matches := tagRe.FindAllStringSubmatchIndex(text, -1)
	for _, m := range matches {
		result = append(result, entry{
			text:  strings.TrimPrefix(text[m[0]:m[1]], "#"),
			start: int64(len(text[0:m[0]])),
			end:   int64(len(text[0:m[1]]))},
		)
	}
	return result
}

func makeXRPCC(cfg *config) (*xrpc.Client, error) {
	xrpcc := &xrpc.Client{
		Client: cliutil.NewHttpClient(),
		Host:   cfg.Host,
		Auth:   &xrpc.AuthInfo{Handle: cfg.Handle},
	}

	auth, err := comatproto.ServerCreateSession(context.TODO(), xrpcc, &comatproto.ServerCreateSession_Input{
		Identifier: xrpcc.Auth.Handle,
		Password:   cfg.Password,
	})
	if err != nil {
		return nil, fmt.Errorf("cannot create session: %w", err)
	}
	xrpcc.Auth.Did = auth.Did
	xrpcc.Auth.AccessJwt = auth.AccessJwt
	xrpcc.Auth.RefreshJwt = auth.RefreshJwt
	return xrpcc, nil
}

func addLink(xrpcc *xrpc.Client, post *bsky.FeedPost, link string) {
	doc, _ := goquery.NewDocument(link)
	var title string
	var description string
	var imgURL string
	if doc != nil {
		title = doc.Find(`title`).Text()
		description, _ = doc.Find(`meta[property="description"]`).Attr("content")
		imgURL, _ = doc.Find(`meta[property="og:image"]`).Attr("content")
		if title == "" {
			title, _ = doc.Find(`meta[property="og:title"]`).Attr("content")
			if title == "" {
				title = link
			}
		}
		if description == "" {
			description, _ = doc.Find(`meta[property="og:description"]`).Attr("content")
			if description == "" {
				description = link
			}
		}
		post.Embed.EmbedExternal = &bsky.EmbedExternal{
			External: &bsky.EmbedExternal_External{
				Description: description,
				Title:       title,
				Uri:         link,
			},
		}
	}
	if imgURL != "" && post.Embed.EmbedExternal != nil {
		resp, err := http.Get(imgURL)
		if err == nil {
			defer resp.Body.Close()
			b, err := ioutil.ReadAll(resp.Body)
			if err == nil {
				resp, err := comatproto.RepoUploadBlob(context.TODO(), xrpcc, bytes.NewReader(b))
				if err == nil {
					post.Embed.EmbedExternal.External.Thumb = &lexutil.LexBlob{
						Ref:      resp.Blob.Ref,
						MimeType: http.DetectContentType(b),
						Size:     resp.Blob.Size,
					}
				}
			}
		}
	}
}

func doPost(cfg *config, text string) error {
	xrpcc, err := makeXRPCC(cfg)
	if err != nil {
		return fmt.Errorf("cannot create client: %w", err)
	}

	post := &bsky.FeedPost{
		Text:      text,
		CreatedAt: time.Now().Local().Format(time.RFC3339),
	}

	for _, entry := range extractLinksBytes(text) {
		post.Entities = append(post.Entities, &bsky.FeedPost_Entity{
			Index: &bsky.FeedPost_TextSlice{
				Start: entry.start,
				End:   entry.end,
			},
			Type:  "link",
			Value: entry.text,
		})
		if post.Embed == nil {
			post.Embed = &bsky.FeedPost_Embed{}
		}
		if post.Embed.EmbedExternal == nil {
			addLink(xrpcc, post, entry.text)
		}
	}

	for _, entry := range extractTagsBytes(text) {
		post.Facets = append(post.Facets, &bsky.RichtextFacet{
			Features: []*bsky.RichtextFacet_Features_Elem{
				{
					RichtextFacet_Tag: &bsky.RichtextFacet_Tag{
						Tag: entry.text,
					},
				},
			},
			Index: &bsky.RichtextFacet_ByteSlice{
				ByteStart: entry.start,
				ByteEnd:   entry.end,
			},
		})
	}

	resp, err := comatproto.RepoCreateRecord(context.TODO(), xrpcc, &comatproto.RepoCreateRecord_Input{
		Collection: "app.bsky.feed.post",
		Repo:       xrpcc.Auth.Did,
		Record: &lexutil.LexiconTypeDecoder{
			Val: post,
		},
	})
	if err != nil {
		return fmt.Errorf("failed to create post: %w", err)
	}
	fmt.Println(resp.Uri)

	return nil
}

func main() {
	var skip bool
	var dsn string
	var feedURL string
	var format string
	var pattern string
	var re *regexp.Regexp
	var cfg config
	var ver bool

	flag.BoolVar(&skip, "skip", false, "Skip tweet")
	flag.StringVar(&dsn, "dsn", os.Getenv("FEED2BSKY_DSN"), "Database source")
	flag.StringVar(&feedURL, "feed", "", "Feed URL")
	flag.StringVar(&format, "format", "{{.Title | normalize}}\n{{.Link}}", "Tweet Format")
	flag.StringVar(&pattern, "pattern", "", "Match pattern")
	flag.StringVar(&cfg.Host, "host", os.Getenv("FEED2BSKY_HOST"), "Bluesky host")
	flag.StringVar(&cfg.Handle, "handle", os.Getenv("FEED2BSKY_HANDLE"), "Bluesky handle")
	flag.StringVar(&cfg.Password, "password", os.Getenv("FEED2BSKY_PASSWORD"), "Bluesky password")
	flag.BoolVar(&ver, "v", false, "show version")
	flag.Parse()

	if ver {
		fmt.Println(version)
		os.Exit(0)
	}

	var err error
	if pattern != "" {
		re, err = regexp.Compile(pattern)
		if err != nil {
			log.Fatal(err)
		}
	}

	funcMap := template.FuncMap{
		"normalize": func(s string) string {
			// Remove invisible Unicode characters and squeeze multiple newlines
			s = regexp.MustCompile(`[\p{Cf}]`).ReplaceAllString(s, "")
			s = regexp.MustCompile(`\n\n+`).ReplaceAllString(s, "\n")
			return s
		},
	}
	t := template.Must(template.New("").Funcs(funcMap).Parse(format))

	db, err := sql.Open("postgres", dsn)
	if err != nil {
		log.Fatal(err)
	}

	bundb := bun.NewDB(db, pgdialect.New())
	defer bundb.Close()

	_, err = bundb.NewCreateTable().Model((*Feed2Bsky)(nil)).IfNotExists().Exec(context.Background())
	if err != nil {
		log.Println(err)
		return
	}

	feed, err := gofeed.NewParser().ParseURL(feedURL)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return
	}

	sort.Slice(feed.Items, func(i, j int) bool {
		return feed.Items[i].PublishedParsed.After(*feed.Items[j].PublishedParsed)
	})

	for _, item := range feed.Items {
		if item == nil {
			break
		}

		fi := Feed2Bsky{
			Feed: feedURL,
			GUID: item.GUID,
		}
		_, err := bundb.NewInsert().Model(&fi).Exec(context.Background())
		if err != nil {
			if !strings.Contains(err.Error(), "duplicate key value violates unique constraint") {
				log.Println(err)
			}
			continue
		}

		var buf bytes.Buffer
		err = t.Execute(&buf, &item)
		if err != nil {
			log.Println(err)
			continue
		}

		content := buf.String()

		if re != nil {
			if !re.MatchString(content) {
				continue
			}
		}

		if skip {
			log.Printf("%q", content)
			continue
		}

		err = doPost(&cfg, content)
		if err != nil {
			log.Println(err)
			continue
		}
	}
}
