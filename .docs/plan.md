# Plan: `shast` – Shell-Command-Typing-Game (Go, TUI, Docker-Sandbox)

## Context
Das Repo `/home/user/Dokumente/gits/shast` ist leer: Branch `master` ohne Commits.
Ziel ist ein Terminal-Spiel als einzelnes Go-Binary, gebaut nach der Original-Spec
(Task-Text des Users). Mode 1 (Speed Typing) wird implementiert. Mode 2 (Reverse
Challenge) wird architektonisch vorbereitet und bleibt ein dokumentierter Stub.
Umgebung: Go 1.27.1 und Docker 29.8.1 sind vorhanden. `staticcheck` liegt in
`~/go/bin`, fehlt aber im PATH der Agent-Shell. Deshalb wird es über die
`tool`-Direktive in `go.mod` eingebunden (siehe M0).

### Entscheidungen des Users (fix)
- Modulpfad `shast`, Commits auf Branch `main` (`git branch -m master main` vor dem ersten Commit).
- **Ein Sandbox-Container pro Session.** `/work` (tmpfs) wird vor jedem Befehl aus dem Read-only-`/seed` zurückgesetzt.
- **Katalog:** Default-YAML per `go:embed`. `--catalog DIR` ergänzt sie bzw. überschreibt Einträge anhand der ID, ohne Rebuild.
- **Setup:** TUI-Startmenü, vorbelegt durch CLI-Flags. Mode 2 ist sichtbar, aber deaktiviert („coming soon“).
- **Tippfehler:** Weitertippen ist erlaubt, falsche Zeichen erscheinen rot, Korrektur per Backspace. Enter zählt nur bei exaktem Match.
- **Reihenfolge konfigurierbar:** `--order random|difficulty` (auch im Setup-Menü). Bei `random` gibt es keine Wiederholungen, `--seed N` macht die Reihenfolge reproduzierbar. `difficulty` heißt easy→medium→hard, innerhalb einer Stufe zufällig.

### Defaults (vom User akzeptiert)
- UI-Sprache Englisch.
- WPM = (Zeichen/5)/Minuten, gemessen vom ersten Tastendruck bis Enter.
- Accuracy = korrekte Anschläge / alle Anschläge (korrigierte Fehler zählen mit). Errors = Anzahl falscher Anschläge.
- Highscores als JSON in `$XDG_DATA_HOME/shast/scores.json` (Fallback `~/.local/share`), Top 10 nach Session-Score (WPM × Accuracy), mit Filter-Metadaten.
- Nur nicht-interaktive Befehle, kein stdin (also kein `less`, `top`, `vim`).
- TTY-Rendering im Viewport: `\r`, `\n`, `\b` und SGR-Farben. Alle anderen Steuersequenzen werden entfernt. Kein VT-Emulator.

## Libraries (mit Begründung)
| Zweck | Library | Warum |
|---|---|---|
| TUI | `charm.land/bubbletea/v2` (v2.0.x), `charm.land/bubbles/v2` (viewport), `charm.land/lipgloss/v2` | De-facto-Standard, aktiv gepflegt, Elm-Architektur passt zu testbarem State, Resize-Events und Viewport sind eingebaut |
| ANSI | `github.com/charmbracelet/x/ansi` | Stabiler Parser zum Strippen und Sanitizen (wird von bubbletea ohnehin mitgezogen) |
| Docker | `github.com/moby/moby/client` + `github.com/moby/moby/api` | Offizieller Nachfolger von `docker/docker` (eingefroren auf v28). Exec mit TTY, ConsoleSize/ExecResize, ImageBuild, Labels |
| YAML | `go.yaml.in/yaml/v3` | Gepflegter Nachfolger von `gopkg.in/yaml.v3`, `KnownFields(true)` für strikte Validierung |
| Lint | `honnef.co/go/tools/cmd/staticcheck` via `go get -tool` | Versionsgepinnt, `go tool staticcheck ./...`, unabhängig vom PATH |

Keine CLI-Frameworks: Subcommands laufen über das stdlib-Paket `flag` mit je einem `FlagSet`.

## Architektur / Layout (go.dev/doc/modules/layout, „command + internal“)
```
main.go                      Subcommand-Dispatch: play (default), verify, image build, expected
internal/
  outcmp/     Normalize(ANSI strip, \r\n→\n, CR-overwrite, trailing ws, trailing newlines), Equal(a,b,Options{IgnoreOrder})
  catalog/    Challenge, Difficulty, Load(fs.FS) + Merge, Validate, Filter; commands.yaml + expected/<id>.txt (embedded)
  typing/     Pure State: Target, Input, per-char Status (Correct/Wrong/Pending), Keystroke/Backspace, Matches()
  score/      RoundStats/SessionStats (WPM, Accuracy, Errors, Durations); Highscore Store (JSON, atomic write)
  engine/     Mode interface, SpeedMode, ReverseMode stub; Session (Auswahl/Order/Seed, Round-Lifecycle)
  sandbox/    image/ (Dockerfile, seed.sh, gitconfig – embedded), Image tag = hash(context);
              Session container: Start/Reset/Run(stream)/Resize/Close; Preflight; Sweep stale containers
  verify/     Katalog-Verifikation + Expected-Output-Generierung (nutzt sandbox + outcmp)
  tui/        Bubble-Tea-Models: setup, round (typing → running → result), summary; ansi-Renderer für Output
```
**Kleine Interfaces am Verwendungsort:** `tui` und `verify` definieren jeweils
`type runner interface { Reset(ctx) error; Run(ctx, cmd string, size Size, out io.Writer) (Result, error) }`.
Das hält Unit-Tests ohne Docker möglich (Fake-Runner).

### Engine / Mode-2-Vorbereitung
```go
type Challenge struct {
    ID, Category, Explanation string
    Difficulty    Difficulty   // easy|medium|hard
    Command       string       // Mode 1: Zielbefehl (Mode 2: Referenzlösung, optional)
    Expected      string       // Mode 2: erwartete Ausgabe (aus expected/<id>.txt)
    Deterministic bool         // default true; false für ps, date, uptime, ...
    Compare       outcmp.Options
}
type Mode interface {
    Name() string
    Eligible(Challenge) bool                 // Speed: Command != ""; Reverse: Deterministic && Expected != ""
    Prompt(Challenge) string                 // Speed: Command; Reverse: Expected
    CanSubmit(ch Challenge, input string) bool // Speed: input == Command; Reverse: input != ""
    Judge(ch Challenge, input string, res sandbox.Result) Verdict // Reverse: outcmp.Equal
}
```
`ReverseMode` existiert mit Doc-Comment. Eligible/Prompt/CanSubmit sind ausimplementiert,
weil sie trivial sind. Das Setup-Menü bietet den Modus aber noch nicht an (README beschreibt, was noch fehlt: Freitext-Eingabe-UI).

### Katalog-Format (`internal/catalog/commands.yaml`)
```yaml
- id: top-client-ips
  command: "awk '{print $1}' logs/access.log | sort | uniq -c | sort -rn | head -5"
  category: text           # files|search|text|json|archive|git|permissions|disk|process|network-config|system
  difficulty: medium
  explanation: "Counts requests per client IP and shows the five busiest."
  deterministic: true      # optional, default true
  compare: { ignore_order: false }  # optional
  exit_code: 0             # optional, default 0 (für verify)
```
Validierung: eindeutige IDs, Pflichtfelder, gültige Difficulty/Kategorie, kein Zeilenumbruch
im Befehl, nur druckbares ASCII (tippbar), maximale Länge ≤ 120 Zeichen (passt in 80x24 auf 2 Zeilen).
Bei `--catalog DIR` werden `DIR/commands.yaml` und optional `DIR/expected/` über denselben
`Load(fs.FS)`-Pfad geladen und per ID gemergt.
Mindestens 60 Befehle, verteilt auf alle Kategorien und Schwierigkeiten.
Ungefähr 5 davon sind `deterministic: false` (`ps aux`, `date`, `uptime`, `free -h`, `df -h`).

## Sandbox
**Image** (`internal/sandbox/image/`, embedded, gebaut über `shast image build` per ImageBuild-API):
- `debian:trixie-slim`, per Digest gepinnt. Pakete: coreutils, findutils, grep, sed, gawk, jq, tar, gzip, xz-utils, zip/unzip, git, procps, tree, file, bc, less (nicht im Katalog), util-linux, diffutils, tini, locales-all oder C.UTF-8, bsdextrautils (column).
- User `dev` (uid/gid 1000), `/etc/gitconfig` mit `core.pager=cat`, `color.ui=never`, `safe.directory=*`, fixem Namen und fixer E-Mail.
- `seed.sh` läuft beim Build als `dev` und erzeugt `/seed` **deterministisch** ohne `$RANDOM`, mit arithmetisch erzeugten Mustern:
  `app/` (src, scripts), `logs/` (access.log ~500 Zeilen, app.log mit Levels, rotierte `.gz` per `gzip -n`),
  `configs/` (nginx.conf, app.yaml, `.env`), `data/` (users.csv, orders.json, servers.json),
  Dateien mit gestaffelten Größen, Rechte (600 für Secrets, 755 für Skripte) und mtimes per `touch -d` über mehrere Tage.
  Das Git-Repo hat ~8 Commits mit fixem `GIT_AUTHOR_DATE`/`GIT_COMMITTER_DATE` und Autor, dadurch sind die Hashes stabil.
  Zum Schluss schreibt das Skript ein Manifest `/seed.sha256` (sortierte Liste mit Pfad, Modus, mtime, sha256).
- Image-Tag `shast-sandbox:<sha256(context)[:12]>`: Das Binary erkennt ein fehlendes oder veraltetes Image.

**Container** (`HostConfig`), pro Session einer:
`NetworkMode: none`, `User: 1000:1000`, `CapDrop: ALL`, `SecurityOpt: no-new-privileges`,
`ReadonlyRootfs`, tmpfs `/work` (64m, uid=1000, mode 0755) und `/tmp` (16m),
`PidsLimit 128`, `Memory 256m` (+ Swap gleich), `NanoCPUs 1e9`, `Init: true` (tini),
`AutoRemove: true`, Label `shast.session=<id>`, `Hostname: web01`.
Env: `TZ=UTC`, `LANG=LC_ALL=C.UTF-8`, `HOME=/tmp`, `PAGER=GIT_PAGER=cat`, `TERM=xterm-256color`.
Der Hauptprozess ist `sleep 14400`. Das ist der Watchdog: Selbst nach `kill -9` des Spiels verschwindet der Container nach spätestens 4 h.

**Reset** (Exec vor jedem Befehl):
`kill -9 -1` (beendet übrig gebliebene Hintergrundprozesse des Users) → `chmod -R u+rwX /work` → `rm -rf /work/..?* /work/.[!.]* /work/*`
→ `cp -a /seed/. /work/` (mtimes bleiben erhalten). Danach ist `/work` identisch zum Seed.

**Run:** Exec mit `bash --noprofile --norc -c 'timeout -s KILL <N>s bash -c "$0"' <cmd>`
(Befehl als Argument, kein String-Escaping), `WorkingDir /work`, `Tty: true`, `ConsoleSize` = Viewport-Größe
(Verify und Mode 2 fest 80x24), kein stdin. Die Ausgabe wird live an einen `io.Writer` gestreamt.
Clientseitig gibt es einen Output-Cap (1 MiB): Beim Überschreiten wird abgebrochen und per Reset-Kill beendet, das Ergebnis wird als truncated markiert.
Zusätzlich gibt es einen Context-Timeout (Befehlstimeout + 2 s). Exit-Code kommt über ExecInspect, die Dauer wird gemessen.

**Cleanup:** `Close()` per `defer` in `main`. `signal.NotifyContext` fängt SIGINT, SIGTERM und SIGHUP.
Ctrl+C in der TUI beendet das Programm regulär, danach läuft der defer.
Beim Start entfernt `Sweep` alle Container mit dem Label `shast.session`, die nicht zur aktuellen Session gehören.
**Preflight:** Ist Docker nicht erreichbar, erscheint die Meldung „Docker daemon not reachable: … (is Docker running? are you in the docker group?)“.
Fehlt das Image: „sandbox image shast-sandbox:<tag> not found – run `shast image build`“. In beiden Fällen Exit 1, **bevor** die TUI startet.

## Determinismus & Verifikation
- `shast verify [--runs 2] [--id X]`: Pro Durchlauf gibt es eine frische Session. Jeder Befehl läuft zweimal in derselben Session (prüft den Reset) mit Größe 80x24.
  Geprüft werden der Exit-Code (== `exit_code`) und dass die Ausgabe nicht leer ist, sofern nicht `allow_empty`.
  Bei deterministischen Befehlen müssen alle Läufe nach `outcmp.Normalize` identisch sein und mit `expected/<id>.txt` übereinstimmen, falls vorhanden.
  Nicht-deterministische Befehle prüfen nur den Exit-Code. Ausgabe: Tabelle mit PASS/FAIL, Exit 1 bei einem Fehler.
- `shast expected --out internal/catalog/expected` erzeugt Expected-Outputs für alle deterministischen Befehle (normalisiert gespeichert).
- Seed-Determinismus: `image build --no-cache` zweimal ausführen, dann muss `/seed.sha256` identisch sein (Integrationstest bzw. Make-Target).
- **Hinweis/Pushback:** Über Image-Rebuilds mit neueren apt-Paketversionen kann die Ausgabe driften, z. B. bei `tree` oder `jq`.
  Deshalb ist der Base-Image-Digest gepinnt, und die Expected-Outputs gehören zum Image-Tag. `verify` erkennt Drift.
  Bei TTY-Ausgabe ist das Format von `ls` spaltenbreitenabhängig, daher gilt die feste Größe 80x24 für Verify und Mode 2.
- **Regel:** Scheitert ein Befehl, werden Befehl oder Seed angepasst, **nie** die Sandbox-Restriktionen.

## TUI (Mode 1), ausgelegt auf 80x24
- **Setup:** Mode (Speed | Reverse [disabled]), Difficulty (all/easy/medium/hard), Category (all/…), Rounds (5/10/20), Order (random/difficulty).
  Vorbelegt über Flags `--difficulty --category --rounds --order --seed --catalog --timeout`. Zeigt eine Warnung, wenn der Filter weniger Befehle ergibt als Runden gewählt sind.
- **Round:** Header (Runde x/y · Kategorie · Difficulty), Zielbefehl mit Soft-Wrap, bei dem jedes Zeichen grün/rot/grau eingefärbt ist (Leerzeichen-Fehler als roter Unterstrich),
  Live-Stats (WPM, Accuracy, Zeit), Output-Viewport (Rest der Höhe) und Footer mit Tastenhinweisen.
  Der Timer startet beim ersten Tastendruck. Eingaben über Ziellänge + 10 werden ignoriert. Enter ohne exaktem Match bewirkt nichts (kurzer Hinweis).
  Tab und Esc werden nicht als Eingabe gewertet: Esc bricht die Runde ab, Ctrl+C beendet das Spiel.
- **Running/Result:** Die Ausgabe streamt über einen Goroutine→`tea.Msg`-Kanal live in den Viewport, der automatisch mitscrollt.
  Mit PgUp/PgDn und ↑↓ lässt sich scrollen. Danach erscheinen Exit-Code, Ausführungszeit, `[truncated]`/`[timeout]` und die Erklärung. Enter führt zur nächsten Runde.
- **Summary:** Tabelle pro Runde, Session-Totals, Highscore-Platzierung (wird gespeichert), Top 10.
- **Resize:** `tea.WindowSizeMsg` sorgt für Relayout und `ExecResize` bei laufendem Befehl.
  Unter 60x16 erscheint der Hinweis „terminal too small“.

## Meilensteine (jeder endet mit `make check` grün + Commit auf `main`)
- **M0 Scaffold:** `go mod init shast`, `go get -tool honnef.co/go/tools/cmd/staticcheck`,
  `Makefile` (`check` = `gofmt -l` muss leer sein + `go vet ./...` + `go tool staticcheck ./...` + `go test ./...`),
  `.gitignore`, `main.go` mit Subcommand-Gerüst, README-Skelett.
- **M1 `outcmp`:** Normalize/Equal + table-driven Tests (ANSI, CRLF, CR-Overwrite, trailing ws, ignore_order, Unicode).
- **M2 `catalog`:** Typen, Loader (`fs.FS`), Merge, Validate, Filter + Tests (gültig/ungültig/unknown fields/Duplikate/Merge/Filter).
  Erste `commands.yaml` mit ≥ 60 Einträgen.
- **M3 `typing` + `score`:** Tippzustand, Statistiken, Highscore-Store (Temp-Dir-Tests) + table-driven Tests.
- **M4 Sandbox-Image:** Dockerfile, `seed.sh`, `shast image build`, Tag-Hash, Manifest. Integrationstest: Seed-Manifest ist über zwei Builds hinweg stabil.
- **M5 Sandbox-Runtime:** Start/Reset/Run/Resize/Close, Preflight, Sweep. Integrationstests (Skip ohne Docker, Skip mit Hinweis bei fehlendem Image, `-short` überspringt):
  kein Netz, `whoami`=dev, Rootfs nicht beschreibbar, kein sudo/setuid, pids-Limit, Timeout, Output-Cap, Reset nach `rm -rf *`/`chmod 000`, Hintergrundprozesse sterben, Aufräumen nach Close.
- **M6 Verify + Expected:** `shast verify`, `shast expected`. Katalog und Seed werden iteriert, bis alle ≥ 60 Befehle zweimal in frischen Sessions bestehen. Expected-Dateien werden committet.
  Ein Integrationstest ruft `verify` auf.
- **M7 `engine`:** Mode-Interface, SpeedMode, ReverseMode-Stub, Session-Auswahl (Filter, Order, Seed, keine Wiederholung) + Tests mit Fake-Runner.
- **M8 `tui`:** Setup, Round, Result, Summary, ANSI-Renderer (Tests für Renderer und Model-Update-Logik), Highscore-Integration, Resize.
  Manueller Test bei 80x24.
- **M9 Doku & Feinschliff:** README (Voraussetzungen, `make`, `shast image build`, Spielen, Befehl hinzufügen, Verify/Expected, Mode-2-Design), finaler `make check`.

## Verifikation (End-to-End)
1. `make check`: gofmt, vet, staticcheck und alle Unit-Tests grün. Integrationstests laufen mit Docker und werden sonst übersprungen.
2. `go build -o shast . && ./shast image build && ./shast verify --runs 2`: Alle Befehle sind PASS, deterministische Ausgaben sind identisch und entsprechen den Expected-Dateien.
3. `docker stop` der Engine bzw. ein falscher Image-Tag muss zur klaren Fehlermeldung führen, bevor die TUI startet.
4. `./shast` in einem 80x24-Terminal: Session mit 3 Runden durchspielen, Resize während des Streamings testen, Ctrl+C mitten im Befehl drücken.
   Danach muss `docker ps -a --filter label=shast.session` leer sein.
5. Ein destruktiver Befehl (z. B. `find . -name '*.log' -delete`) muss in der nächsten Runde wieder den vollständigen Seed vorfinden.

## Für die ausführenden Agents
- Alle Tests table-driven. Fehler mit `fmt.Errorf("…: %w", err)` wrappen, `context` für Timeouts und Cancel verwenden, keine globalen Singletons.
- Keine Features über die Spec hinaus (siehe `CLAUDE.md`: KISS/YAGNI).
- Scheitert ein Katalogbefehl, wird der Befehl oder die Seed-Datei geändert, nie die Isolation.
- Commit-Messages enden mit der Co-Authored-By-Zeile aus der System-Attribution.

## Abweichungen
Anpassungen gegenüber dem Plan, die sich bei der Umsetzung als nötig erwiesen haben (jeweils die kleinste spec-konforme Änderung):

- **Kein `tini`-Paket im Image (M4):** Der Container läuft mit `Init: true`. Docker startet dann sein eigenes `docker-init` (tini) als PID 1, ein zweites tini im Image wäre ungenutzt. `C.UTF-8` ist in trixie-slim bereits enthalten, deshalb fehlt auch `locales-all`.
- **Seed-Determinismus-Test als Opt-in (M4):** Zwei `--no-cache`-Builds dauern jeweils ca. 45 s und brauchen Netz für apt. Deshalb läuft `TestSeedDeterministic` nur mit `SHAST_SEED_REBUILD=1` (`make seed-determinism`) und nicht bei jedem `make check`.
- **Feste Git-Zeitstempel auch für Tag, Checkout und Reflog (M4):** Nicht nur Commits, auch das annotierte Tag, `git checkout` (Reflog) und `git init` bekommen feste `GIT_*_DATE`. Der Index wird per `git read-tree HEAD` ohne Stat-Daten neu aufgebaut, weil er sonst Inode und ctime des Builds enthält.
- **apt aus snapshot.debian.org (M4):** Der gepinnte Digest allein fixiert die Tool-Versionen nicht, weil `apt-get update` den Live-Mirror nutzt (jq driftete in 8 Tagen von `+deb13u3` auf `+deb13u4`). Das Dockerfile schaltet deshalb auf das Snapshot-Datum um, das `debian.sources` des Base-Images nennt. Zusätzlich werden alle setuid/setgid-Bits im Image entfernt (Defense in Depth zu `no-new-privileges`).
- **Golden-Manifest (M4):** `internal/sandbox/testdata/seed.sha256` ist committet. `TestSeedManifestGolden` vergleicht es bei jedem `make check` mit dem Image (Skip ohne Docker oder Image) und wird nach gewollten Seed-Änderungen mit `-update` neu geschrieben.
