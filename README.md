# feed2bsky

Post bluesky entry via RSS feed.

## Usage

```
feed2bsky -dsn $DATABASE_URL \
    -feed https://vim-jp.org/rss.xml \
    -format '{{.Title}}{{"\n"}}{{.Link}} #vimeditor'
```

Or kubernetes cronjob.

```yaml
apiVersion: batch/v1
kind: CronJob
metadata:
  name: vim-jp-feed-bot
spec:
  schedule: '0 * * * *'
  successfulJobsHistoryLimit: 1
  failedJobsHistoryLimit: 1
  jobTemplate:
    spec:
      backoffLimit: 1
      template:
        spec:
          containers:
          - name: vim-jp-feed-bot
            image: mattn/feed2bsky
            imagePullPolicy: IfNotPresent
            #imagePullPolicy: Always
            command: ["/go/bin/feed2bsky"]
            args:
            - '-feed'
            - 'https://vim-jp.org/rss.xml'
            - '-format'
            - '{{.Title}}{{\"\n\"}}{{.Link}} #vimeditor'
            env:
            - name: FEED2BSKY_HOST
              valueFrom:
                configMapKeyRef:
                  name: vim-jp-feed-bot
                  key: feed2bsky-host
            - name: FEED2BSKY_HANDLE
              valueFrom:
                configMapKeyRef:
                  name: vim-jp-feed-bot
                  key: feed2bsky-handle
            - name: FEED2BSKY_PASSWORD
              valueFrom:
                configMapKeyRef:
                  name: vim-jp-feed-bot
                  key: feed2bsky-password
            - name: FEED2BSKY_DSN
              valueFrom:
                configMapKeyRef:
                  name: vim-jp-feed-bot
                  key: feed2bsky-dsn
          restartPolicy: Never
```

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: vim-jp-feed-bot
data:
  feed2bsky-host: 'https://bsky.social'
  feed2bsky-handle: 'mattn.bsky.social'
  feed2bsky-password: 'XXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXX'
  feed2bsky-dsn: 'postgres://user:password@server/database'
```

## Installation

```
$ go install github.com/mattn/feed2bsky@latest
```

Or use `mattn/feed2bsky` for docker image.

## License

MIT

## Author

Yasuihro Matsumoto (a.k.a. mattn)
