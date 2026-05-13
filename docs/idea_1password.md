# 1Password 連携に関する設計メモ

Veil に 1Password を連携する案は、1Password を secret file の保存先にするというより、iCloud 上の暗号化 blob を復号するための key provider として使う方が自然に見える。

ただし、この構成は既存の symlink 方式へ暗号化を足すだけでは済まない。保存形式、workspace への出し方、更新、削除、巻き戻し運用まで含めて、扱うべき状態がかなり増える。

## 主要な難しさ

### symlink 方式と materialize 方式の差

従来の symlink 方式では、workspace 上の file を編集すると store 側の実体もそのまま更新される。

暗号化 store では、workspace には復号済み file を実体として書き出す必要がある。そのため、編集した内容は自動では暗号化 blob に戻らない。

この差により、少なくとも次の設計が必要になる可能性がある。

- `update` のような明示的な再暗号化操作
- `vanish` / TTL 時の変更検知
- 変更済み file を誤って削除しないための lease / hash 管理

### 1Password は認証境界であり、filesystem ではない

1Password CLI から鍵を読む UX は、1Password が unlock 済みなら自然だが、lock 中は処理が止まる。

Veil 側で master password や session token を長期保持しない前提にすると、失敗時の扱いが重要になる。

- 鍵取得に失敗しても workspace file を壊さない
- 1Password の stderr や secret 値を不用意に出さない
- test 用の `VEIL_DATA_KEY` と本番の 1Password key を混同しない

### 暗号化 blob と metadata の整合性

暗号化 store は最低でも ciphertext と metadata を持つ。これらが別 file になると、部分的な書き込み失敗や process kill で不整合が起きる。

特に難しいのは、更新中に既存 secret を壊さないこと。

考慮が必要な例:

- blob は新しいが metadata は古い
- metadata は新しいが blob が存在しない
- metadata 書き込み失敗時に旧 blob を消してしまう
- versioned blob を採用した場合の古い blob の prune

安全側に寄せるなら、metadata を commit pointer として扱い、最後に metadata だけを atomic に差し替える形が分かりやすい。ただし power loss まで考えるなら fsync や recovery path も論点になる。

### rollback mirror の維持は単純な dual-write ではない

一時的に `VeilStoreEncripted` を main としつつ、旧 symlink 方式へ戻せるよう `VeilStore` に plaintext mirror を残す運用は便利だが、整合性の問題を生む。

難しい点:

- `add` / `update` 時に encrypted store と plaintext mirror の両方を更新する必要がある
- 片方の更新だけ失敗した場合、どちらを正とするか決める必要がある
- 旧ブランチへ戻して `VeilStore` 側だけ更新した後、再び暗号化ブランチへ戻った場合の conflict
- `remove` / `purge` 時に versioned blob や mirror をどこまで消すか

rollback 可能性を維持するなら、mirror は単なる backup ではなく同期対象として扱う必要がある。

### 削除は保存より危険

暗号化 store では削除対象が増える。

- metadata
- current blob
- old versioned blobs
- plaintext mirror
- workspace 上の materialized file
- state / lease

metadata が壊れている時に metadata だけ削除すると、versioned blob への pointer を失い、暗号化 secret が orphan として残る。削除系コマンドは「消せないなら止める」方が安全な場面が多い。

### TTL は file の存在だけでは判断できない

TTL で materialized file を消すには、その file が Veil によって出されたものか、ユーザーが後で作った別 file なのかを区別する必要がある。

そのため、state には少なくとも次の情報が欲しい。

- materialized path
- materialized 時点の plaintext hash
- expires_at

hash が変わっている場合は削除せず、`modified` として残す方が安全。ただし modified lease を残すと TTL cleaner の再実行間隔や busy loop も考える必要がある。

## 現時点の見立て

1Password を key provider として使う方向性自体は妥当。

一方で、暗号化 store は既存 symlink store の差し替えではなく、別の store model として扱うべきである。特に materialize / update / vanish / remove / purge / migrate の各操作で、平文 file と暗号化 blob のどちらが正なのかを明確にしないと、UX と安全性の両方が崩れやすい。

MVP を進めるなら、最初に保証範囲を小さく定義した方がよい。
