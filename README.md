# FindSenryu4Discord

<p align="center">
  <img src="./.github/img/haiku.png" width="200" /><br />
  Discordで川柳を検出します
</p>

## Invite

<p align="center">
  <a href="https://discordapp.com/api/oauth2/authorize?client_id=480281065588785162&permissions=378880&scope=bot">
    <img width="400" src="./.github/img/discord-logo.png">
  </a>
</p>

## Commands

### メッセージコマンド

```
詠め
```

> 今までにギルド内で詠まれた句をもとに、新しい川柳を生成します。

```
詠むな
```

> 理不尽な要求なので、最後に詠んだ人とその内容を晒しあげます。

### スラッシュコマンド

```
/mute
```

> このチャンネルでの川柳検出をミュートします。

```
/unmute
```

> このチャンネルでの川柳検出のミュートを解除します。

```
/rank
```

> ギルド内で詠んだ回数が多い人のランキングを表示します。

```
/delete [user]
```

> 自分の川柳を選択して削除します。サーバー管理者は `user` オプションで他ユーザーの川柳も削除できます。

```
/detect on | off | status
```

> 自分の川柳検出のオン/オフをサーバー単位で切り替えます。`status` で現在の設定を確認できます。

```
/contact
```

> Bot管理者にお問い合わせを送信します。モーダルで件名と内容を入力でき、管理チャンネルに転送されます。`contact_channel_id` が設定されている場合のみ利用可能です。

### 管理者コマンド

管理用ギルド (`admin.guild_id`) でのみ使用可能です。`admin.owner_ids` に登録されたユーザーのみ実行できます。

```
/admin stats
```

> Botの稼働状況（稼働時間・接続サーバー数・DB統計）を表示します。

```
/admin guilds
```

> 接続中の全サーバー一覧を表示します。

```
/admin backup
```

> データベースのバックアップを手動で作成します（SQLite のみ）。

## Self-hosting

### 設定

`sample.config.toml` を `config.toml` にコピーして編集してください。

```toml
[discord]
token = ""       # Discord Bot トークン（必須）
playing = ""     # Botのプレイ中ステータス

[database]
driver = "sqlite3"  # sqlite3 or postgres
path = "data/senryu.db"
# dsn = "host=localhost user=findsenryu dbname=findsenryu sslmode=disable"

[log]
level = "info"   # debug, info, warn, error
format = "text"  # json, text

[admin]
owner_ids = []         # Bot管理者のDiscord ID
guild_id = ""          # 管理コマンド登録先ギルドID
log_channel_id = ""    # サーバー参加通知・日次サマリー送信先
contact_channel_id = "" # /contact コマンドのお問い合わせ通知先

[server]
enabled = true   # ヘルスチェック/メトリクスサーバー
port = 9090

[backup]
enabled = false
interval_hour = 24
path = "data/backups"
max_backups = 7
```

環境変数 `FINDSENRYU_` プレフィックスで設定を上書きできます（例: `FINDSENRYU_DISCORD_TOKEN`）。

### 機能

- **川柳検出** — メッセージから5-7-5の音節パターンを自動検出・記録
- **チャンネルミュート** — チャンネル単位で検出を無効化
- **お問い合わせ** — `/contact` でBot管理者にフィードバックや問い合わせを送信
- **ユーザーオプトアウト** — ユーザー単位・サーバー単位で検出を無効化
- **自動バックアップ** — SQLite データベースの定期バックアップ（設定で有効化）
- **インテリジェンス通知** — サーバー参加通知と日次サマリーを管理チャンネルに送信
- **ヘルスチェック** — `/health`, `/ready`, `/stats` エンドポイント
- **Prometheus メトリクス** — `/metrics` エンドポイントで各種メトリクスを公開
