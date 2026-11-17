# mktauto

Post to your [MKTAuto](https://mktauto.site) accounts from a folder on your computer: choose the project, the accounts
and the files (in your order, and which file goes to which account), add the caption, link and first comment, then save
a draft, schedule it or publish now. It warns you when a file was already posted.

Windows, macOS and Linux. Downloads and install commands: **https://mktauto.site/download**

## Install

```sh
# Linux and macOS
curl -fsSL https://mktauto.site/install.sh | sh
```

```powershell
# Windows (PowerShell)
irm https://mktauto.site/install.ps1 | iex
```

Both download from [GitHub Releases](https://github.com/abdullahalnuman0/mktauto-cli/releases), check the SHA-256 against
the release's `checksums.txt` and install for your user. Or download a file from the releases page yourself.
Update later with `mktauto update` (it also tells you once a day when a new version is out;
`MKTAUTO_NO_UPDATE_CHECK=1` turns that off).

The programs are not code-signed yet: Windows SmartScreen or macOS Gatekeeper may warn about a file downloaded in a
browser (Windows: More info → Run anyway; macOS: System Settings → Privacy & Security → Open Anyway). Check the
checksum first. Company-managed computers may block unsigned programs.

## Use

```sh
mktauto login            # approve this computer in the browser (or: mktauto login --code mkl_…)
cd ~/Pictures/launch     # a folder with images or videos
mktauto                  # menu → "Post from this folder"
```

`mktauto login` shows a short code and a QR code: approve in the browser that opens, or scan the QR code with your
phone (its camera opens the approval page; the MKTAuto app scans it in Terminal access). `MKTAUTO_NO_ANIMATION=1`
turns off the animated logo at the start.

The wizard asks, in this order: project → **what you're posting** (images, a video, images and videos together, or
text only) → the files (with their order) → the accounts (only those that can post that content are listed, and
they're ticked for you) → files used before → video cover → which files go to which account → caption, hashtags,
link, first comment, "made with AI" → a different caption for some accounts (each starts empty, from a copy of the
main caption, or keeps what you wrote) → TikTok / YouTube / Pinterest settings → when → a review of the whole post.

- **Esc** goes back to the previous question, with your answers kept; **Ctrl+C** cancels. Nothing is saved until
  you confirm on the review screen.
- Files already posted from the folder are marked; captions show their length against each platform's limit
  (X 280, Threads 500, Instagram and TikTok 2,200…) and an account whose caption is too long is offered its own.

Everything can also run without questions, e.g. from a script:

```sh
mktauto post --project Memoshot --accounts facebook,instagram,tiktok \
  --files cover.png,shot1.png,shot2.png --only tiktok=1 \
  --caption-file caption.txt --hashtags android,kotlin --first-comment "Download: https://…" \
  --tiktok-privacy SELF_ONLY --when "2026-12-01 18:30"
```

`mktauto post -h` lists every flag, grouped, with examples. Any account can get its own values with
`ACCOUNT=value` flags: `--caption-for instagram="…"`, `--title-for youtube="…"`, `--tiktok-privacy-for @me=SELF_ONLY`,
`--board-for pinterest=<id>`, `--tiktok-brand yours,partner` and more.

### Many posts at once

```sh
mktauto batch                       # step by step: one post per image, video or subfolder
mktauto batch --each video --project Memoshot --accounts instagram,tiktok --when next-slot --tiktok-privacy SELF_ONLY
mktauto batch --each folder --project Memoshot --accounts facebook --when "2026-12-01 10:00" --every 2d
mktauto import plan.csv             # one post per spreadsheet row; mktauto import --example prints one
```

Each post's caption comes from its own `.txt` (or `caption.txt` in a subfolder), else `--caption` (`{name}` = the
file name). The whole plan is shown before anything is uploaded; files already posted from the folder are skipped.

### Templates, hashtag sets, account groups, AI

Saved on the website, used here: the wizard offers templates first, account groups at the top of the account list
(ticking one ticks its accounts) and hashtag sets next to the hashtags. In scripts: `--template "Weekly tip"`,
`--hashtag-set Android`, `--accounts group:Launch`. With the AI helper set up on the server, the wizard offers
"AI caption help" (improve, shorten to fit, tone, translate, hashtags), an account's own caption can be shortened by
AI, and `mktauto ai shorten "…" --platform x` works in scripts.

### Posts dashboard

`mktauto posts` (or "Saved posts" in the menu): every recent post with its details, tabs by status, `/` to search,
and one key to act: `s` schedule / move, `u` unschedule, `p` publish now, `e` edit caption, `c` cancel, `d` delete,
`r` retry, `o` open. In every list of the wizard, `/` searches too.

### Calendar, results, inbox, repost and repeat

```sh
mktauto calendar                    # the next 14 days, day by day, with the free posting times
mktauto stats                       # the last 30 days: reach, likes, comments… per platform, top posts
mktauto stats last --refresh        # one post, each account, fresh numbers from the platforms
mktauto stats --links               # clicks on tracked links, by source
mktauto inbox                       # failures, posts to finish yourself, approvals (enter: what to do)
mktauto duplicate last --next-slot  # post it again: a copy, scheduled in the next free time
mktauto post … --when "2026-12-01 18:00" --repeat weekly --repeat-until 2027-03-01
mktauto stop-repeat 3f2a91c0
```

The review screen (and `--dry-run`) shows the exact text each account gets, with the hashtags added and its length
against the platform's limit.

### Tab completion

```sh
echo 'eval "$(mktauto completion bash)"' >> ~/.bashrc       # bash
echo 'eval "$(mktauto completion zsh)"' >> ~/.zshrc         # zsh (after compinit)
mktauto completion fish > ~/.config/fish/completions/mktauto.fish
mktauto completion powershell >> $PROFILE                  # Windows PowerShell
```

Tab then completes commands, flags, your projects, accounts (`facebook,ins<Tab>`), groups, templates, hashtag sets,
files and recent posts.

Find names and ids for scripts: `mktauto projects`, `mktauto accounts --project Memoshot`, `mktauto boards @account`
(all with `--json`).

Manage saved posts (`POST` = its id, the first characters `mktauto status` shows, or `last`):

```sh
mktauto status                      # recent posts
mktauto schedule last --at "2026-12-01 18:30"   # or --next-slot; also moves a scheduled post
mktauto unschedule 3f2a91c0         # back to a draft
mktauto edit last --caption-file new.txt --hashtags "android kotlin"
mktauto publish last --wait
mktauto cancel 3f2a91c0 --yes       # stop what isn't published yet
mktauto delete 3f2a91c0 --yes       # drafts only
```

The menu's "Saved posts" does the same step by step. Other commands: `retry`, `history`, `open`, `whoami`, `logout`,
`doctor`, `update`.

### Folder helpers

- `reel.txt` next to `reel.mp4` (or `caption.txt` in the folder) fills in the caption.
- `mktauto.yml` holds defaults for the folder:

```yaml
project: Memoshot
accounts: [instagram, tiktok]
hashtags: [android, productivity]
madeWithAI: false
when: next-slot   # draft, now, next-slot or "2026-12-01 18:30"
```

## Security

- The tool contains no passwords or keys. `mktauto login` creates a terminal session that you can see and revoke on the
  website (Settings → Terminal access); it can be limited to drafts, some projects or a number of posts a day.
- The session token is kept in the system keychain (Windows Credential Manager, macOS Keychain, Linux Secret Service),
  or in a file only your user can read when there is no keychain (`mktauto doctor` says which).
- Files go straight to storage over HTTPS; social media passwords and platform tokens never reach your computer.
- Environment: `MKTAUTO_SERVER` (another MKTAuto server), `MKTAUTO_TOKEN` (a session token for CI),
  `MKTAUTO_CONFIG_DIR`, `MKTAUTO_TOKEN_STORE=file`, `MKTAUTO_NO_BROWSER=1`.

## Develop

```sh
go test ./...
go build -ldflags "-X main.version=dev" -o mktauto ./cmd/mktauto
```

## Release

```sh
git tag v1.2.0 && git push origin v1.2.0
```

GitHub Actions runs the tests, builds Windows, macOS and Linux (x64 and ARM64), writes `checksums.txt` and publishes
the release (a tag with a `-`, e.g. `v1.3.0-beta.1`, becomes a pre-release). Earlier releases stay.
