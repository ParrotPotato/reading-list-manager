# Running it as a service

`tmux` is fine for trying something out, but a tmux session does not survive a
reboot — which is why a kernel update leaves your site down until you notice.
systemd starts the app at boot, restarts it if it crashes, and collects its
logs. No container required: the binary is statically linked and has no cgo, so
there is nothing to install alongside it.

To run it in a container instead, see
[Running it in Docker](../README.md#running-it-in-docker) and skip this page.

## 1. Build the binary

On the server:

```sh
cd ~/reading-list-manager
go build -o catalogue .
```

Or build it on your laptop and copy it over — it cross-compiles to a static
Linux binary with no dependencies:

```sh
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o catalogue-linux .
scp catalogue-linux nitesh@72.62.31.251:~/reading-list-manager/catalogue
```

Use the built binary, never `go run`, for a service. `go run` compiles to a
temporary directory and runs the result as a *child* process, so systemd ends
up supervising the compiler rather than the app.

## 2. Install the unit

```sh
sudo cp deploy/catalogue.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now catalogue
```

`enable` is what makes it start at boot; `--now` also starts it immediately.

## 3. Check it

```sh
systemctl status catalogue
journalctl -u catalogue -f
curl -sI 127.0.0.1:8080          # expect 303 to /login
```

## Everyday commands

```sh
sudo systemctl restart catalogue   # after deploying a new binary
sudo systemctl stop catalogue
journalctl -u catalogue --since "1 hour ago"
journalctl -u catalogue -p err     # errors only
```

## Deploying a new version

```sh
cd ~/reading-list-manager && git pull
go build -o catalogue .
sudo systemctl restart catalogue
```

A restart takes milliseconds, and Caddy returns a brief 502 if a request lands
in the gap.

## Account commands with the service running

They are safe to run against the live database — SQLite in WAL mode handles
several processes, and the app sets a busy timeout:

```sh
cd ~/reading-list-manager
./catalogue -db reading-list.db -add-user someone@example.com
./catalogue -db reading-list.db -list-users
```

## Backups

Stop the service first so the WAL is checkpointed and the `.db` is
self-contained, or use SQLite's own online backup:

```sh
sudo systemctl stop catalogue
cp reading-list.db ~/backups/catalogue-$(date +%F).db
sudo systemctl start catalogue
```

## Doing the same for your other apps

Copy `catalogue.service`, and change four things: `Description`, `User`,
`WorkingDirectory`, and `ExecStart`. Give each app its own port, matching the
port its Caddy block proxies to.

```sh
sudo cp deploy/catalogue.service /etc/systemd/system/watchers.service
sudo nano /etc/systemd/system/watchers.service   # edit the four lines
sudo systemctl daemon-reload
sudo systemctl enable --now watchers
```

Then stop running them in tmux, so you do not end up with two copies competing
for the same port.

## Checking nothing is left in tmux

```sh
tmux ls
systemctl list-units --type=service --state=running | grep -E "catalogue|watchers|caddy"
sudo ss -lntp | grep -E ':8080|:8001'
```

The last one should show the services holding those ports, not a tmux shell.
