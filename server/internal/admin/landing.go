package admin

import (
	"encoding/json"
	"html/template"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"
)

type downloadFile struct {
	Name  string
	URL   string
	Label string
	Class string
	Icon  template.HTML
	Badge string
}

var landingTmpl = template.Must(template.New("landing").Parse(`<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>Proxyness — Secure TLS proxy</title>
<meta name="description" content="Fast, private, undetectable TLS proxy. System-wide VPN or browser-only mode. macOS and Windows.">
<style>
* { margin: 0; padding: 0; box-sizing: border-box; }
html, body { background: #07080f; color: #e6e8ef; font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, Inter, sans-serif; -webkit-font-smoothing: antialiased; line-height: 1.5; }
a { color: inherit; text-decoration: none; }
.wrap { max-width: 1040px; margin: 0 auto; padding: 0 24px; }

/* Hero */
.hero { position: relative; padding: 96px 0 72px; text-align: center; overflow: hidden; }
.hero::before { content: ""; position: absolute; inset: 0; background: radial-gradient(ellipse at 50% 0%, rgba(59,130,246,0.18), transparent 60%), radial-gradient(ellipse at 80% 20%, rgba(16,185,129,0.12), transparent 55%); pointer-events: none; }
.hero > * { position: relative; }
.eyebrow { display: inline-block; font-size: 0.75rem; letter-spacing: 2px; text-transform: uppercase; color: #6b7280; padding: 6px 12px; border: 1px solid #1f2433; border-radius: 999px; margin-bottom: 24px; }
h1 { font-size: clamp(2.2rem, 5vw, 3.6rem); font-weight: 800; letter-spacing: -0.02em; line-height: 1.1; margin-bottom: 20px; background: linear-gradient(135deg, #60a5fa 0%, #34d399 100%); -webkit-background-clip: text; -webkit-text-fill-color: transparent; background-clip: text; }
.lede { max-width: 620px; margin: 0 auto 40px; color: #9aa3b2; font-size: 1.15rem; }
.downloads { display: flex; flex-wrap: wrap; gap: 14px; justify-content: center; margin-bottom: 18px; }
.download-btn { display: inline-flex; align-items: center; gap: 12px; padding: 14px 22px; border-radius: 12px; font-size: 0.98rem; font-weight: 600; transition: transform 0.15s, box-shadow 0.15s, border-color 0.15s; background: #10131e; border: 1px solid #232838; color: #fff; }
.download-btn:hover { transform: translateY(-2px); box-shadow: 0 10px 30px rgba(0,0,0,0.4); border-color: #3b82f6; }
.download-btn .icon { font-size: 1.3rem; }
.download-btn .badge { font-size: 0.72rem; background: #1b2030; padding: 3px 8px; border-radius: 6px; color: #8c95a8; }
.hint { color: #5b6476; font-size: 0.85rem; }

/* Language switcher */
.lang { position: fixed; top: 20px; right: 20px; display: inline-flex; padding: 3px; background: rgba(13,16,26,0.85); backdrop-filter: blur(8px); border: 1px solid #1f2433; border-radius: 999px; z-index: 10; }
.lang button { background: transparent; border: none; color: #6b7280; font-size: 0.78rem; font-weight: 600; letter-spacing: 0.5px; padding: 6px 12px; border-radius: 999px; cursor: pointer; transition: color 0.15s, background 0.15s; }
.lang button.active { background: #1a3a5c; color: #fff; }

/* Sections */
section { padding: 72px 0; }
.section-title { text-align: center; font-size: 2rem; font-weight: 700; letter-spacing: -0.01em; margin-bottom: 14px; }
.section-sub { text-align: center; color: #7b8494; max-width: 560px; margin: 0 auto 48px; }

/* Feature grid */
.features { display: grid; grid-template-columns: repeat(auto-fit, minmax(280px, 1fr)); gap: 16px; }
.feature { background: linear-gradient(180deg, #0d101a 0%, #0a0d16 100%); border: 1px solid #1a1f2e; border-radius: 16px; padding: 24px; transition: border-color 0.2s, transform 0.2s; }
.feature:hover { border-color: #2a3347; transform: translateY(-2px); }
.feature .ico { width: 40px; height: 40px; border-radius: 10px; background: rgba(59,130,246,0.12); display: flex; align-items: center; justify-content: center; margin-bottom: 16px; font-size: 1.2rem; }
.feature h3 { font-size: 1.05rem; font-weight: 700; margin-bottom: 8px; color: #e6e8ef; }
.feature p { color: #8b93a4; font-size: 0.92rem; line-height: 1.55; }

/* Stats strip */
.stats { display: flex; flex-wrap: wrap; justify-content: center; gap: 8px 56px; padding: 28px 24px; margin: 0 auto; max-width: 860px; border-top: 1px solid #12161f; border-bottom: 1px solid #12161f; }
.stat { text-align: center; }
.stat .num { font-size: 1.6rem; font-weight: 800; background: linear-gradient(135deg, #60a5fa, #34d399); -webkit-background-clip: text; -webkit-text-fill-color: transparent; background-clip: text; }
.stat .lbl { font-size: 0.78rem; color: #6b7280; text-transform: uppercase; letter-spacing: 1px; margin-top: 2px; }

/* Benchmarks */
.bench { overflow-x: auto; border: 1px solid #1a1f2e; border-radius: 16px; background: #0b0e18; }
.bench table { width: 100%; border-collapse: collapse; font-size: 0.92rem; }
.bench th, .bench td { padding: 14px 18px; text-align: left; border-bottom: 1px solid #12161f; }
.bench th { color: #8b93a4; font-weight: 600; font-size: 0.78rem; text-transform: uppercase; letter-spacing: 0.8px; background: #0d101a; }
.bench td:first-child { color: #e6e8ef; font-weight: 500; }
.bench td.win { color: #34d399; font-weight: 700; }
.bench td.loss { color: #6b7280; }
.bench tr:last-child td { border-bottom: none; }
.bench-note { text-align: center; color: #5b6476; font-size: 0.82rem; margin-top: 14px; }

/* How */
.steps { display: grid; grid-template-columns: repeat(auto-fit, minmax(240px, 1fr)); gap: 16px; counter-reset: step; }
.step { background: #0b0e18; border: 1px solid #1a1f2e; border-radius: 16px; padding: 28px 24px; position: relative; }
.step::before { counter-increment: step; content: counter(step); position: absolute; top: -14px; left: 24px; width: 32px; height: 32px; background: linear-gradient(135deg, #3b82f6, #10b981); color: #06080f; border-radius: 50%; display: flex; align-items: center; justify-content: center; font-weight: 800; font-size: 0.95rem; }
.step h4 { font-size: 1rem; font-weight: 700; margin: 6px 0 8px; }
.step p { color: #8b93a4; font-size: 0.9rem; }

/* Footer */
footer { padding: 48px 0 64px; text-align: center; color: #4a5263; font-size: 0.85rem; border-top: 1px solid #12161f; margin-top: 40px; }
footer a { color: #6b7687; }
footer a:hover { color: #9aa3b2; }

@media (max-width: 640px) {
  .hero { padding: 64px 0 48px; }
  section { padding: 56px 0; }
}
</style>
</head>
<body>

<div class="lang" role="group" aria-label="Language">
  <button type="button" data-lang="en" class="active">EN</button>
  <button type="button" data-lang="ru">RU</button>
</div>

<section class="hero">
  <div class="wrap">
    <span class="eyebrow" data-en="Custom stack · macOS · Windows" data-ru="Собственный стек · macOS · Windows">Custom stack · macOS · Windows</span>
    <h1 data-en="A proxy that looks&lt;br&gt;like the rest of the web" data-ru="Прокси, которого&lt;br&gt;не видно в сети">A proxy that looks<br>like the rest of the web</h1>
    <p class="lede" data-en="Flexible setup, no reconnects." data-ru="Гибкая настройка без переподключений.">Flexible setup, no reconnects.</p>
    <div class="downloads">
      {{range .}}<a href="{{.URL}}" class="download-btn {{.Class}}">
        <span class="icon">{{.Icon}}</span> <span data-en="Download for {{.Label}}" data-ru="Скачать для {{.Label}}">Download for {{.Label}}</span> <span class="badge">{{.Badge}}</span>
      </a>
      {{end}}
    </div>
  </div>
</section>

<section>
  <div class="wrap">
    <h2 class="section-title" data-en="Only what you need — through the proxy. Any time." data-ru="Только то, что нужно — через прокси. В любой момент.">Only what you need — through the proxy. Any time.</h2>
    <p class="section-sub" data-en="A full custom stack — not a wrapper around an off-the-shelf tunnel." data-ru="Полностью свой стек — не обёртка над готовым решением.">A full custom stack — not another wrapper around an off-the-shelf tunnel.</p>
    <div class="features">
      <div class="feature">
        <div class="ico">&#128737;</div>
        <h3 data-en="Invisible on port 443" data-ru="Невидим на порту 443">Invisible on port 443</h3>
        <p data-en="Everything rides a single TLS stream on the standard HTTPS port. To any network or DPI box in between, it looks exactly like a normal web server." data-ru="Весь трафик идёт одним TLS-потоком через стандартный HTTPS-порт. Для любой сети и любого DPI по пути это обычный веб-сервер.">Everything rides a single TLS stream on the standard HTTPS port. To any network or DPI box in between, it looks exactly like a normal web server.</p>
      </div>
      <div class="feature">
        <div class="ico">&#9889;</div>
        <h3 data-en="AutoTransport — UDP or TLS" data-ru="AutoTransport — UDP или TLS">AutoTransport — UDP or TLS</h3>
        <p data-en="Two transports, picked automatically. Custom UDP with ARQ and BBR/CUBIC congestion control for 1.5–2× faster TTFB; TLS when heavy downloads benefit from kernel TCP. No tuning, no toggles." data-ru="Два транспорта с автоматическим выбором. Свой UDP с ARQ и congestion-control BBR/CUBIC даёт TTFB в 1.5–2 раза быстрее; TLS выигрывает на тяжёлых загрузках. Без настроек и тумблеров.">Two transports, picked automatically. Custom UDP with ARQ and BBR/CUBIC congestion control for 1.5–2× faster TTFB; TLS when heavy downloads benefit from kernel TCP. No tuning, no toggles.</p>
      </div>
      <div class="feature">
        <div class="ico">&#127760;</div>
        <h3 data-en="Hybrid TUN + Browser" data-ru="Гибридный TUN + Браузер">Hybrid TUN + Browser</h3>
        <p data-en="In TUN mode, apps go through the tunnel while browsers ride a local SOCKS5 — avoiding QUIC headaches. Or flip to Browser-only when that is all you need." data-ru="В режиме TUN приложения идут через тоннель, а браузеры — через локальный SOCKS5 (чтобы не упираться в QUIC). Или включи режим «только браузер», если этого достаточно.">In TUN mode, apps go through the tunnel while browsers ride a local SOCKS5 — avoiding QUIC headaches. Or flip to Browser-only when that is all you need.</p>
      </div>
      <div class="feature">
        <div class="ico">&#128230;</div>
        <h3 data-en="Per-app split tunneling" data-ru="Раздельный тоннель по приложениям">Per-app split tunneling</h3>
        <p data-en="Pick exactly which apps go through the tunnel. Telegram and Discord over the proxy, games direct — your call." data-ru="Выбирай, какие приложения идут через тоннель. Telegram и Discord — через прокси, игры — напрямую. Ты решаешь.">Pick exactly which apps go through the tunnel. Telegram and Discord over the proxy, games direct — your call.</p>
      </div>
      <div class="feature">
        <div class="ico">&#128721;</div>
        <h3 data-en="Kill Switch built in" data-ru="Kill Switch из коробки">Kill Switch built in</h3>
        <p data-en="Three independent health detectors watch the tunnel in real time. If it drops, traffic is blocked instantly — your real IP never leaks. When the link recovers, traffic resumes on its own." data-ru="Три независимых детектора следят за состоянием тоннеля в реальном времени. При обрыве весь трафик мгновенно блокируется — реальный IP не утекает. После восстановления связь возобновляется сама.">Three independent health detectors watch the tunnel in real time. If it drops, traffic is blocked instantly — your real IP never leaks. When the link recovers, traffic resumes on its own.</p>
      </div>
      <div class="feature">
        <div class="ico">&#128274;</div>
        <h3 data-en="Hardware-bound access" data-ru="Привязка к устройству">Hardware-bound access</h3>
        <p data-en="Your key pairs with your machine on first use. Nobody else can walk off with your access — even if they get the key." data-ru="При первом подключении ключ привязывается к твоей машине. Никто чужой не уйдёт с твоим доступом — даже если получит ключ.">Your key pairs with your machine on first use. Nobody else can walk off with your access — even if they get the key.</p>
      </div>
    </div>
  </div>
</section>

<section>
  <div class="wrap">
    <h2 class="section-title" data-en="Benchmarks" data-ru="Тесты">Benchmarks</h2>
    <p class="section-sub" data-en="Real benchmarks from the same network, same server region, same time of day." data-ru="Реальные замеры на одной сети, одном регионе сервера, в одно и то же время.">Real benchmarks from the same network, same server region, same time of day.</p>
    <div class="bench">
      <table>
        <thead>
          <tr>
            <th data-en="Metric" data-ru="Метрика">Metric</th>
            <th>Proxyness</th>
            <th>WireGuard</th>
            <th>Outline</th>
          </tr>
        </thead>
        <tbody>
          <tr><td data-en="Ping 8.8.8.8" data-ru="Пинг 8.8.8.8">Ping 8.8.8.8</td><td class="win">61 ms</td><td class="loss">100 ms</td><td class="loss">—</td></tr>
          <tr><td data-en="DNS (avg)" data-ru="DNS (среднее)">DNS (avg)</td><td class="win">63 ms</td><td class="loss">104 ms</td><td class="loss">—</td></tr>
          <tr><td data-en="TTFB github.com" data-ru="TTFB github.com">TTFB github.com</td><td class="win">0.38 s</td><td class="loss">0.49 s</td><td class="loss">timeout</td></tr>
          <tr><td data-en="TTFB telegram.org" data-ru="TTFB telegram.org">TTFB telegram.org</td><td class="win">0.43 s</td><td class="loss">0.70 s</td><td class="loss">timeout</td></tr>
          <tr><td data-en="Download" data-ru="Загрузка">Download</td><td class="win">5.0 MB/s</td><td class="loss">2.6 MB/s</td><td class="loss">~12 KB/s</td></tr>
          <tr><td data-en="Upload" data-ru="Отдача">Upload</td><td class="loss">4.6 MB/s</td><td class="win">6.3 MB/s</td><td class="loss">—</td></tr>
        </tbody>
      </table>
    </div>
    <p class="bench-note" data-en="Outline on Shadowsocks times out on HTTPS under Russian ISPs in 2026. Proxyness uses real TLS on 443 — it looks like a web server." data-ru="Outline на Shadowsocks в 2026 году таймаутится на HTTPS у российских провайдеров. Proxyness использует настоящий TLS на 443 — выглядит как обычный веб-сервер.">Outline on Shadowsocks times out on HTTPS under Russian ISPs in 2026. Proxyness uses real TLS on 443 — it looks like a web server.</p>
  </div>
</section>

<section>
  <div class="wrap">
    <h2 class="section-title" data-en="Three steps to online" data-ru="Три шага до подключения">Three steps to online</h2>
    <p class="section-sub" data-en="No configuration files, no command line, no browser extensions required." data-ru="Без конфигов, без командной строки, без расширений для браузера.">No configuration files, no command line, no browser extensions required.</p>
    <div class="steps">
      <div class="step">
        <h4 data-en="Install the app" data-ru="Установи приложение">Install the app</h4>
        <p data-en="Grab the installer for your platform above and run it. Takes less than a minute." data-ru="Скачай установщик для своей системы выше и запусти. Занимает меньше минуты.">Grab the installer for your platform above and run it. Takes less than a minute.</p>
      </div>
      <div class="step">
        <h4 data-en="Enter your access key" data-ru="Введи ключ доступа">Enter your access key</h4>
        <p data-en="Paste the key you were given. It binds to your device automatically on first connect." data-ru="Вставь выданный ключ. При первом подключении он автоматически привяжется к твоему устройству.">Paste the key you were given. It binds to your device automatically on first connect.</p>
      </div>
      <div class="step">
        <h4 data-en="Click Connect" data-ru="Нажми Connect">Click Connect</h4>
        <p data-en="That's it. Your traffic is now flowing through the tunnel. Change modes any time from the header." data-ru="Готово. Трафик идёт через тоннель. Режим можно переключить в любой момент из шапки приложения.">That's it. Your traffic is now flowing through the tunnel. Change modes any time from the header.</p>
      </div>
    </div>
  </div>
</section>

<footer>
  <div class="wrap">Proxyness</div>
</footer>

<script>
(function(){
  var saved = localStorage.getItem("lang");
  var lang = saved || (navigator.language && navigator.language.toLowerCase().startsWith("ru") ? "ru" : "en");
  function apply(l){
    document.documentElement.lang = l;
    document.querySelectorAll("[data-en]").forEach(function(el){
      var v = el.getAttribute("data-" + l);
      if (v != null) el.innerHTML = v;
    });
    document.querySelectorAll(".lang button").forEach(function(b){
      b.classList.toggle("active", b.getAttribute("data-lang") === l);
    });
    localStorage.setItem("lang", l);
  }
  document.querySelectorAll(".lang button").forEach(function(b){
    b.addEventListener("click", function(){ apply(b.getAttribute("data-lang")); });
  });
  apply(lang);
})();
</script>

</body>
</html>`))

func LandingHandler(downloadsDir string) http.Handler {
	cache := &releaseCache{}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		files := cache.get()
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		landingTmpl.Execute(w, files)
	})
}

const githubRepo = "ilyasmurov/proxyness"

type releaseCache struct {
	mu      sync.Mutex
	files   []downloadFile
	fetched time.Time
}

func (c *releaseCache) get() []downloadFile {
	c.mu.Lock()
	defer c.mu.Unlock()
	if time.Since(c.fetched) < 5*time.Minute && c.files != nil {
		return c.files
	}
	files := fetchGitHubAssets()
	if files != nil {
		c.files = files
		c.fetched = time.Now()
	}
	if c.files != nil {
		return c.files
	}
	return fallbackDownloads()
}

type ghRelease struct {
	Assets []ghAsset `json:"assets"`
}

type ghAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

func fetchGitHubAssets() []downloadFile {
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get("https://api.github.com/repos/" + githubRepo + "/releases/latest")
	if err != nil {
		log.Printf("[landing] github API error: %v", err)
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		log.Printf("[landing] github API status: %d", resp.StatusCode)
		return nil
	}

	var rel ghRelease
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		log.Printf("[landing] github API decode error: %v", err)
		return nil
	}

	var result []downloadFile
	for _, a := range rel.Assets {
		lower := strings.ToLower(a.Name)
		switch {
		case strings.HasSuffix(lower, ".pkg") && strings.Contains(lower, "arm64"):
			result = append(result, downloadFile{
				Name: a.Name, URL: a.BrowserDownloadURL,
				Label: "macOS Apple Silicon", Class: "mac",
				Icon: iconApple, Badge: ".pkg",
			})
		case strings.HasSuffix(lower, ".exe") && strings.Contains(lower, "setup"):
			result = append(result, downloadFile{
				Name: a.Name, URL: a.BrowserDownloadURL,
				Label: "Windows", Class: "win",
				Icon: iconWindows, Badge: ".exe",
			})
		}
	}
	return result
}

const iconApple template.HTML = `<svg width="18" height="18" viewBox="0 0 24 24" fill="currentColor" aria-hidden="true"><path d="M17.05 12.536c-.02-2.09 1.71-3.095 1.789-3.145-.974-1.424-2.494-1.62-3.035-1.644-1.293-.13-2.524.76-3.182.76-.658 0-1.672-.74-2.75-.72-1.414.02-2.72.822-3.447 2.088-1.47 2.55-.376 6.324 1.057 8.396.7 1.013 1.535 2.152 2.633 2.111 1.058-.04 1.457-.684 2.735-.684 1.278 0 1.637.684 2.754.664 1.138-.02 1.858-1.03 2.554-2.048.805-1.176 1.135-2.316 1.155-2.376-.025-.012-2.215-.85-2.263-3.402zM15.02 6.37c.584-.706.978-1.688.87-2.664-.84.034-1.858.56-2.462 1.266-.54.626-1.013 1.626-.886 2.584.94.072 1.893-.478 2.478-1.186z"/></svg>`

const iconWindows template.HTML = `<svg width="18" height="18" viewBox="-14 -14 116 116" fill="currentColor" aria-hidden="true"><path d="M0 12.402 35.687 7.51v34.422H0zm0 63.195L35.687 80.49V46.507H0zm39.6 5.432L87.6 87.6V46.506H39.6zm0-73.63v35.99H87.6V0z"/></svg>`

func fallbackDownloads() []downloadFile {
	ghBase := "https://github.com/" + githubRepo + "/releases/latest"
	return []downloadFile{
		{URL: ghBase, Label: "macOS Apple Silicon", Class: "mac", Icon: iconApple, Badge: ".pkg"},
		{URL: ghBase, Label: "Windows", Class: "win", Icon: iconWindows, Badge: ".exe"},
	}
}
