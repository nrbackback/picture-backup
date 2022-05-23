package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"gopkg.in/yaml.v2"

	"github.com/nichanglan/weibo-notify/sender"
	"github.com/nichanglan/weibo-notify/sender/email"

	gt "github.com/mangenotwork/gathertool"
	"github.com/sirupsen/logrus"
	prefixed "github.com/x-cray/logrus-prefixed-formatter"
)

type Config struct {
	Email       email.EmailConfig `yaml:"email"`
	Following   map[int64]string  `yaml:"following"`
	SubURL      string            `yaml:"sub_url"`
	Interval    int64             `yaml:"interval"`
	StartOffset int64             `yaml:"start_offset"`
}

var config Config
var globalStartTime = time.Now().Unix()
var globalEndTime = time.Now().Unix()
var logger *logrus.Logger
var weiboSender sender.Sender

func loadConfig() {
	logger = &logrus.Logger{
		Out:   os.Stderr,
		Level: logrus.DebugLevel,
		Formatter: &prefixed.TextFormatter{
			TimestampFormat: "2006-01-02 15:04:05",
			FullTimestamp:   true,
			ForceFormatting: true,
		},
	}
	configFilePath := flag.String("config", "config.yml", "config file")
	if configFilePath != nil {
		configFile, err := ioutil.ReadFile(*configFilePath)
		if err != nil {
			logger.Fatal(err)
		}
		err = yaml.Unmarshal(configFile, &config)
		if err != nil {
			logger.Fatal(err)
		}
		globalStartTime -= config.StartOffset
	}
	weiboSender = email.EmailSender{
		Conf: config.Email,
	}
}

func main() {
	loadConfig()

	// 每隔 interval 秒查询
	ticker := time.NewTicker(time.Duration(config.Interval * time.Second.Nanoseconds()))
	checkAndSend()
	go func() {
		for {
			select {
			case <-ticker.C:
				checkAndSend()
			}
		}
	}()
	shutdown := make(chan os.Signal, 1)
	signal.Notify(shutdown, syscall.SIGINT, syscall.SIGTERM)
	<-shutdown
}

func checkAndSend() {
	cookie := getCookie()
	for id, name := range config.Following {
		logger.Infof("查找%s在%s至%s的微博", name, time.Unix(globalStartTime, 0).Format("2006-01-02T15:04:05"), time.Unix(globalEndTime, 0).Format("2006-01-02T15:04:05"))
		blogs := latestBlog(id, cookie)
		for _, v := range blogs {
			title := fmt.Sprintf("%s于%s发微博了", name, v.Time)
			logger.Info(title)
			sendHtml(title, v.Content)
		}
	}
	globalStartTime = globalEndTime
	globalEndTime += config.StartOffset
	logger.Info("waiting for next inspection........")
}

func sendHtml(subject, body string) {
	err := weiboSender.Send(subject, body)
	if err != nil {
		logger.Errorf("send error", err)
	}
}

func getCookie() string {
	ctx, err := gt.Get(config.SubURL)
	if err != nil {
		logger.Errorf("get cookie error", err)
		return ""
	}
	v := ctx.Resp.Header.Get("Set-Cookie")
	return v
}

type Blog struct {
	Content string
	Time    string
}

func latestBlog(uid int64, cookie string) (blogs []Blog) {
	client := &http.Client{}
	page := 0
	continued := true
	for continued {
		page++
		url := fmt.Sprintf("https://weibo.com/ajax/statuses/mymblog?uid=%d&page=%d&feature=0", uid, page)
		req, _ := http.NewRequest("GET", url, nil)
		req.Header.Add("accept", "application/json, text/plain, */*")
		req.Header.Add("accept-language", "en-US,en;q=0.9")
		req.Header.Add("cookie", cookie)
		req.Header.Add("referer", fmt.Sprintf("https://weibo.com/u/%d", uid))
		req.Header.Add("sec-ch-ua", "\"Chromium\";v=\"94\", \"Google Chrome\";v=\"94\", \";Not A Brand\";v=\"99\"")
		req.Header.Add("sec-ch-ua-mobile", "?0")
		req.Header.Add("sec-ch-ua-platform", "\"macOS\"")
		req.Header.Add("sec-fetch-dest", "empty")
		req.Header.Add("sec-fetch-mode", "cors")
		req.Header.Add("sec-fetch-site", "same-origin")
		req.Header.Add("user-agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/94.0.4606.81 Safari/537.36")
		req.Header.Add("x-requested-with", "XMLHttpRequest")
		resp, err := client.Do(req)
		if err != nil {
			logger.Errorf("get blof error", err)
		}
		body, err := ioutil.ReadAll(resp.Body)
		if err != nil {
			logger.Errorf("read body error", err)
		}
		var detail RespDetail
		err = json.Unmarshal(body, &detail)
		if err != nil {
			logger.Errorf("unmarshal body %s", string(body), " error", err)
		}
		for _, v := range detail.Data.List {
			sendAt := parseToTimestamp(v.CreatedAt)
			// 忽略置顶微博
			if sendAt < globalStartTime && v.IsTop == 0 {
				continued = false
				break
			}
			if !inDuration(sendAt) {
				continue
			}
			rawWeiboURL := fmt.Sprintf("https://weibo.com/%d/%s", uid, v.Mblogid)
			content := fmt.Sprintf(`<a href="%s">原微博</a>`, rawWeiboURL)
			content += "<br>"
			content += v.Text
			content += "<br>"

			if len(v.PicInfos) != 0 {
				for k, info := range v.PicInfos {
					picURL := info.Bmiddle.URL
					content += fmt.Sprintf(`<img src="%s"  alt="%s" />`, picURL, k)
				}
			}
			picURL := "https://wx2.sinaimg.cn/wap360/"
			if len(v.PicIds) != 0 {
				for _, p := range v.PicIds {
					if v.PicInfos[p].Bmiddle.URL == "" {
						pic := picURL + p + ".jpg"
						content += fmt.Sprintf(`<img src="%s"  alt="%s" />`, pic, p)
					}
				}
			}
			// 转发的
			rs := v.RetweetedStatus
			if rs.ID != 0 {
				content += "------------------"
				rawWeiboURL := fmt.Sprintf("https://weibo.com/%d/%s", rs.User.ID, rs.Mblogid)
				content += "<br>"
				content += fmt.Sprintf(`<a href="%s">被转发微博</a>`, rawWeiboURL)
				content += "<br>"
				content += rs.Text
				content += "<br>"
				if len(rs.PicInfos) != 0 {
					for k, info := range rs.PicInfos {
						picURL := info.Bmiddle.URL
						content += fmt.Sprintf(`<img src="%s"  alt="%s" />`, picURL, k)
					}
				}
			}
			blogs = append(blogs, Blog{
				Content: content,
				Time:    time.Unix(sendAt, 0).Format("15:04:05"),
			})
		}
	}
	return
}

func inDuration(sendAt int64) bool {
	return sendAt >= globalStartTime && sendAt <= globalEndTime
}

func parseToTimestamp(s string) int64 {
	layout := time.RubyDate
	t, err := time.Parse(layout, s)
	if err != nil {
		logger.Errorf("parse time error", err)
	}
	return t.Unix()
}

type RespDetail struct {
	Data struct {
		SinceID string `json:"since_id"`
		List    []struct {
			CreatedAt string `json:"created_at"`
			ID        int64  `json:"id"`
			Mblogid   string `json:"mblogid"`
			User      struct {
				ID int64 `json:"id"`
			} `json:"user"`
			CanEdit         bool               `json:"can_edit"`
			TextRaw         string             `json:"text_raw"`
			Text            string             `json:"text"`
			TextLength      int                `json:"textLength,omitempty"`
			PicIds          []string           `json:"pic_ids"`
			IsTop           int                `json:"isTop,omitempty"`
			PicInfos        map[string]picInfo `json:"pic_infos,omitempty"`
			RepostType      int                `json:"repost_type,omitempty"`
			RetweetedStatus struct {
				CreatedAt string `json:"created_at"`
				ID        int64  `json:"id"`
				Idstr     string `json:"idstr"`
				Mid       string `json:"mid"`
				Mblogid   string `json:"mblogid"`
				User      struct {
					ID int64 `json:"id"`
				} `json:"user"`
				TextRaw    string             `json:"text_raw"`
				Text       string             `json:"text"`
				TextLength int                `json:"textLength"`
				Source     string             `json:"source"`
				PicNum     int                `json:"pic_num"`
				PicInfos   map[string]picInfo `json:"pic_infos"`
			} `json:"retweeted_status,omitempty"`
		} `json:"list"`
	} `json:"data"`
	Ok int `json:"ok"`
}

type picInfo struct {
	Thumbnail struct {
		URL     string `json:"url"`
		CutType int    `json:"cut_type"`
		Type    string `json:"type"`
	} `json:"thumbnail"`
	Bmiddle struct {
		URL     string `json:"url"`
		CutType int    `json:"cut_type"`
		Type    string `json:"type"`
	} `json:"bmiddle"`
	Large struct {
		URL     string `json:"url"`
		CutType int    `json:"cut_type"`
		Type    string `json:"type"`
	} `json:"large"`
	Original struct {
		URL     string `json:"url"`
		CutType int    `json:"cut_type"`
		Type    string `json:"type"`
	} `json:"original"`
	Largest struct {
		URL     string `json:"url"`
		CutType int    `json:"cut_type"`
		Type    string `json:"type"`
	} `json:"largest"`
	Mw2000 struct {
		URL     string `json:"url"`
		CutType int    `json:"cut_type"`
		Type    string `json:"type"`
	} `json:"mw2000"`
	FocusPoint struct {
		Left float64 `json:"left"`
		Top  float64 `json:"top"`
	} `json:"focus_point"`
	ObjectID  string `json:"object_id"`
	PicID     string `json:"pic_id"`
	PhotoTag  int    `json:"photo_tag"`
	Type      string `json:"type"`
	PicStatus int    `json:"pic_status"`
}

// TODO: 有的微博默认折叠了，需要展开
