package updater

import (
"context"
"fmt"
"io"
"os"
"os/exec"
"path/filepath"
"runtime"
"strings"
"sync"
"time"

"github.com/metacubex/mihomo/component/ca"
mihomoHttp "github.com/metacubex/mihomo/component/http"
C "github.com/metacubex/mihomo/constant"
"github.com/metacubex/mihomo/log"

"github.com/metacubex/http"
)

const (
baseReleaseURL    = "https://github.com/2521245392/mihomo/releases/latest/download/"
versionReleaseURL = "https://github.com/2521245392/mihomo/releases/latest/download/version.txt"

baseAlphaURL    = "https://github.com/2521245392/mihomo/releases/latest/download/"
versionAlphaURL = "https://github.com/2521245392/mihomo/releases/latest/download/version.txt"

MaxPackageFileSize = 64 * 1024 * 1024
)

const (
ReleaseChannel = "release"
AlphaChannel   = "alpha"
)

type CoreUpdater struct {
mu sync.Mutex
}

var DefaultCoreUpdater = CoreUpdater{}

func (u *CoreUpdater) CoreBaseName() string {
return "mihomo"
}

func (u *CoreUpdater) Update(currentExePath string, channel string, force bool) (err error) {
u.mu.Lock()
defer u.mu.Unlock()

info, err := os.Stat(currentExePath)
if err != nil {
return fmt.Errorf("check currentExePath %q: %w", currentExePath, err)
}

latestVersion, err := u.getLatestVersion(versionReleaseURL)
if err != nil {
return fmt.Errorf("get latest version: %w", err)
}
log.Infoln("current version %s, latest version %s", C.Version, latestVersion)

if latestVersion == C.Version && !force {
return fmt.Errorf("update error: already using latest version %s", C.Version)
}

defer func() {
if err != nil {
log.Errorln("updater: failed: %v", err)
} else {
log.Infoln("updater: finished")
}
}()

// 1. 直链拉取你自己仓库 Releases 的 UPX 加壳二进制单文件
packageURL := baseReleaseURL + "mihomo"
log.Infoln("updater: updating using url: %s", packageURL)

// 2. 临时文件放入内存目录 /tmp，不占用路由闪存
updateDir := filepath.Join(os.TempDir(), "mihomo-update")
_ = os.RemoveAll(updateDir)
downloadFilePath := filepath.Join(updateDir, "mihomo")

defer u.clean(updateDir)

err = u.download(updateDir, downloadFilePath, packageURL)
if err != nil {
return fmt.Errorf("downloading: %w", err)
}

_ = os.Chmod(downloadFilePath, info.Mode()|0o111)

// 3. 清理软路由上历史残留的备份文件夹，彻底释放空间
workDir := filepath.Dir(currentExePath)
_ = os.RemoveAll(filepath.Join(workDir, "meta-backup"))
_ = os.RemoveAll(filepath.Join(workDir, "backup"))

// 4. 原地替换当前运行的文件（不产生任何备份）
err = u.copyFile(downloadFilePath, currentExePath)
if err != nil {
return fmt.Errorf("replacing: %w", err)
}

return nil
}

func (u *CoreUpdater) getLatestVersion(versionURL string) (version string, err error) {
ctx, cancel := context.WithTimeout(context.Background(), time.Second*10)
defer cancel()
resp, err := mihomoHttp.HttpRequest(ctx, versionURL, http.MethodGet, nil, nil, mihomoHttp.WithCAOption(ca.Option{ZeroTrust: true}))
if err != nil {
return "", err
}
defer resp.Body.Close()

body, err := io.ReadAll(resp.Body)
if err != nil {
return "", err
}
return strings.TrimSpace(string(body)), nil
}

func (u *CoreUpdater) download(updateDir, packagePath, packageURL string) (err error) {
ctx, cancel := context.WithTimeout(context.Background(), time.Second*120)
defer cancel()
resp, err := mihomoHttp.HttpRequest(ctx, packageURL, http.MethodGet, nil, nil, mihomoHttp.WithCAOption(ca.Option{ZeroTrust: true}))
if err != nil {
return fmt.Errorf("http request failed: %w", err)
}
defer resp.Body.Close()

_ = os.MkdirAll(updateDir, 0o755)

wc, err := os.OpenFile(packagePath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o755)
if err != nil {
return fmt.Errorf("os.OpenFile(%s): %w", packagePath, err)
}
defer wc.Close()

_, err = io.Copy(wc, io.LimitReader(resp.Body, MaxPackageFileSize))
if err != nil {
return fmt.Errorf("io.Copy(): %w", err)
}
return nil
}

func (u *CoreUpdater) clean(updateDir string) {
_ = os.RemoveAll(updateDir)
}

func (u *CoreUpdater) copyFile(src, dst string) (err error) {
rc, err := os.Open(src)
if err != nil {
return fmt.Errorf("os.Open(%s): %w", src, err)
}
defer rc.Close()

info, err := rc.Stat()
if err != nil {
return fmt.Errorf("rc.Stat(): %w", err)
}

// 在 Linux 中优先执行 Remove 可以直接 Unlink 运行中的文件，解除 text file busy 锁定
_ = os.Remove(dst)

wc, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, info.Mode())
if err != nil {
return fmt.Errorf("os.OpenFile(%s): %w", dst, err)
}
defer wc.Close()

_, err = io.Copy(wc, rc)
if err != nil {
return fmt.Errorf("io.Copy(): %w", err)
}

if runtime.GOOS == "darwin" {
_ = exec.Command("/usr/bin/codesign", "--sign", "-", dst).Run()
}

log.Infoln("updater: replaced %s successfully", dst)
return nil
}
