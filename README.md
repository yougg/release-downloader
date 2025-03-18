# [Gitea Release Downloader Action](https://github.com/yougg/release-downloader)

An action to support download attachments/source archives from gitea repository release.

## Inputs

The following are optional as `step.with` keys

| Name         | Type    | Description                                                                                                                                                                 |
|--------------|---------|-----------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `token`      | String  | Gitea Token.                                                                                                                                                                |
| `insecure`   | Boolean | Access gitea service without certificate authentication. Defaults to `false`                                                                                                |
| `repository` | String  | Name of a target repository in `<owner>/<repo>` format                                                                                                                      |
| `prerelease` | Boolean | Get releases filter by prerelease, `''`: all releases, `true`: prerelease only, `false`: release only                                                                       |
| `version`    | String  | Version to match the tag list, `''`/`*`/`latest`: match latest, `vX.Y.Z`:fixed version, `v2.3.*`: matched latest                                                            |
| `timeout`    | String  | time limit for requests gitea service, default `''`/`0`: no timeout, ex: `500ms`,`30s`,`10m`,`2h`                                                                           |
| `downloadTo` | String  | Download files to given directory. Defaults to working directory `.`                                                                                                        |
| `sources`    | String  | Download source archive, `''`: skip source, `VERSION.<tar.gz\|zip>`: matched `version`, `<branch\|tag\|SHA>.<tar.gz\|zip>`: download archive by branch/tag name/commit hash | 
| `files`      | String  | Select release attachments, `*`: all files, `example.tar.gz`: fixed file, `*.zip`: matched file list                                                                        |
| `batch`      | String  | JSON string, if `batch` is present, above `repository`,`prerelease`,`version`,`downloadTo`,`sources`,`files` are disabled                                                   |

## Outputs

Get each result in step with these keys: `${{ steps.<step_id>.outputs.<key> }}`

| Name     | Type   | Description                                |
|----------|--------|--------------------------------------------|
| `tag`    | String | the tag which matched version rule         |
| `url`    | String | the release web page url                   |
| `sha`    | String | the commit hash on the matched release tag |
| `commit` | String | the commit html url on the sha             |
| `body`   | String | the release description                    |
| `user`   | String | the release publisher username             |
| `time`   | String | the release publish time                   |
| `stable` | String | mark ✔ for stable release or empty         |

> `outputs` is disabled when batch mode is enabled

## Example usage

```yaml
jobs:
  build:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/setup-go@v5
        with:
          go-version: '>=1.24.1'
      - name: download example
        uses: https://github.com/yougg/release-downloader@main
        with:
          token: '${{ secrets.RELEASE_TOKEN }}' # try '${{ secrets.GITEA_TOKEN }}'
          repository: 'actions/example' # {owner}/{repo}
          prerelease: true   # 留空'': 所有发布版本, true: 仅预发布版本, false: 仅正式发布版本
          version: 'v0.0.*'  # 匹配版本: 留空''/*/latest(取最新发布), 固定版本(ex: v1.2.3), 通配版本(ex: v2.3.*, 取匹配最新的)
          timeout: 0         # 请求超时: 留空''/0(无超时), 指定超时: 120s,5m,3h
          downloadTo: action # 下载保存目录: 留空''/.(当前目录), 指定目录(ex: action)
          sources: main.zip  # 下载源文件包: 留空'': 不下载源文件, VERSION.zip: 同version规则, <branch|tag|SHA>.<tar.gz|zip>: 下载指定分支/标签/Hash对应的压缩包
          files: |-          # 下载文件列表: *: 取所有文件, 固定文件: (ex: example.tar.gz), 通配文件: (ex: *.tar.gz)
            *.tar.gz
            *.zip
      - name: download source archive
        uses: https://github.com/yougg/release-downloader@main
        with:
          token: '${{ secrets.GITEA_TOKEN }}'
          repository: 'actions/example'
          downloadTo: action
          sources: bf63e72696e99c14d1837a5e6ac2372e5f44bc79.zip
```

```yaml
jobs:
  job0:
    runs-on: ubuntu-latest
    outputs:
      TAG: ${{ steps.download0.outputs.tag }}
      SHA: ${{ steps.download0.outputs.sha }}
    steps:
      - uses: actions/setup-go@v5
        with:
          go-version: '>=1.24.1'
      - name: output downloads example
        uses: https://github.com/yougg/release-downloader@main
        id: download0
        with:
          token: '${{ secrets.GITEA_TOKEN }}'
          repository: 'actions/example'
          downloadTo: Downloads
          sources: VERSION.tar.gz
      - name: get output from other step
        run: |
          echo "URL: ${{ steps.download0.outputs.tag }}, Publish Time: ${{ steps.download0.outputs.time }}"
  job1:
    runs-on: ubuntu-latest
    needs: job0
    steps:
      - name: get output from other job
        env:
          TAG: ${{ needs.job0.outputs.TAG }}
          SHA: ${{ needs.job0.outputs.SHA }}
        run: |
          echo "Latest Version: ${TAG}, SHA: ${SHA}"
```

```yaml
jobs:
  build:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/setup-go@v5
        with:
          go-version: '>=1.24.1'
      - name: batch download example
        uses: https://github.com/yougg/release-downloader@main
        with:
          token: '${{ secrets.GITEA_TOKEN }}'
          batch: |-
            [
              {
                "repository": "group0/repoA",
                "prerelease": "true",
                "version": "v1.2.*",
                "downloadTo": "output",
                "sources": "VERSION.tar.gz",
                "files": "aaa*.tar.gz"
              },
              {
                "repository": "group1/repoB",
                "prerelease": "false",
                "version": "v3.4.*",
                "downloadTo": "output",
                "sources": "feat-2.0/fix-xxx.zip",
                "files": "*bbb*.tar.gz"
              },
              {
                "repository": "group2/repoC",
                "prerelease": "",
                "version": "v5.6.*",
                "downloadTo": "output",
                "files": "*.json, *.gz, *.zip"
              }
            ]
```
