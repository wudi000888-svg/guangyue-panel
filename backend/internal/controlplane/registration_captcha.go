package controlplane

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"image"
	"image/color"
	"image/png"
	"math"
	rng "math/rand/v2"
	"net/http"
	"time"
)

const captchaWidth, captchaHeight, captchaPiece = 320, 160, 54
const captchaLifetime = 2 * time.Minute
const captchaCapacity = 1024

type registrationChallenge struct {
	target           int
	client           string
	created, expires time.Time
	verified         bool
}
type captchaPoint struct {
	X float64 `json:"x"`
	T int     `json:"t"`
}

func captchaClient(r *http.Request) string { return digest(requestIP(r) + "/" + r.UserAgent()) }

func (a *App) registrationChallenge(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	settings := a.store.siteSettings()
	if !settings.RegistrationEnabled || !settings.RegistrationCaptcha {
		failure(w, 404, "滑动验证未开放")
		return
	}
	if !a.rateAllowed("captcha:" + requestIP(r)) {
		failure(w, 429, "验证请求过多，请稍后重试")
		return
	}
	var seed [16]byte
	if _, err := rand.Read(seed[:]); err != nil {
		failure(w, 500, "无法生成验证图片，请重试")
		return
	}
	random := rng.New(rng.NewPCG(binary.LittleEndian.Uint64(seed[:8]), binary.LittleEndian.Uint64(seed[8:])))
	target, y := 80+random.IntN(captchaWidth-captchaPiece-90), 24+random.IntN(captchaHeight-captchaPiece-36)
	background, piece, err := captchaImages(random, target, y)
	if err != nil {
		failure(w, 500, "无法生成验证图片，请重试")
		return
	}
	now, client, token := time.Now(), captchaClient(r), randomToken(24)
	a.registrationMu.Lock()
	if a.registrationChallenges == nil {
		a.registrationChallenges = map[string]registrationChallenge{}
	}
	for key, c := range a.registrationChallenges {
		if now.After(c.expires) || c.client == client {
			delete(a.registrationChallenges, key)
		}
	}
	if len(a.registrationChallenges) >= captchaCapacity {
		a.registrationMu.Unlock()
		failure(w, 429, "验证请求过多，请稍后重试")
		return
	}
	a.registrationChallenges[token] = registrationChallenge{target: target, client: client, created: now, expires: now.Add(captchaLifetime)}
	a.registrationMu.Unlock()
	// Only the rendered puzzle is public. Neither the answer nor a clean source
	// image is returned, and the challenge is bound to this client's requests.
	jsonResponse(w, 200, object{"token": token, "width": captchaWidth, "height": captchaHeight, "piece_width": captchaPiece, "piece_y": y, "background": background, "piece": piece, "expires": now.Add(captchaLifetime).Unix()})
}
func validCaptchaTrace(points []captchaPoint, position int, age time.Duration) bool {
	if len(points) < 4 || len(points) > 128 || position < 0 || position > captchaWidth-captchaPiece {
		return false
	}
	last := points[len(points)-1]
	if points[0].T != 0 || math.Abs(points[0].X) > 3 || last.T < 250 || last.T > 120000 || time.Duration(last.T)*time.Millisecond > age+1500*time.Millisecond || math.Abs(last.X-float64(position)) > 3 {
		return false
	}
	previous := -1
	for _, p := range points {
		if math.IsNaN(p.X) || math.IsInf(p.X, 0) || p.X < 0 || p.X > captchaWidth-captchaPiece || p.T < previous || p.T < 0 || p.T > 120000 {
			return false
		}
		previous = p.T
	}
	return true
}
func (a *App) verifyRegistrationChallenge(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	settings := a.store.siteSettings()
	if !settings.RegistrationEnabled || !settings.RegistrationCaptcha {
		failure(w, 404, "滑动验证未开放")
		return
	}
	if !a.rateAllowed("captcha-verify:" + requestIP(r)) {
		failure(w, 429, "验证请求过多，请稍后重试")
		return
	}
	var in struct {
		Token    string         `json:"token"`
		Position int            `json:"position"`
		Trace    []captchaPoint `json:"trace"`
	}
	if !decode(w, r, &in) {
		return
	}
	now, client := time.Now(), captchaClient(r)
	a.registrationMu.Lock()
	c, ok := a.registrationChallenges[in.Token]
	delete(a.registrationChallenges, in.Token) // Every attempt consumes the puzzle.
	valid := ok && !c.verified && c.client == client && now.Before(c.expires) && now.Sub(c.created) >= 250*time.Millisecond && in.Position >= c.target-4 && in.Position <= c.target+4 && validCaptchaTrace(in.Trace, in.Position, now.Sub(c.created))
	proof := ""
	if valid {
		proof = randomToken(32)
		a.registrationChallenges[proof] = registrationChallenge{client: client, created: now, expires: now.Add(captchaLifetime), verified: true}
	}
	a.registrationMu.Unlock()
	if !valid {
		failure(w, 400, "拼图未对齐或已过期，请刷新后重试")
		return
	}
	jsonResponse(w, 200, object{"token": proof, "expires": now.Add(captchaLifetime).Unix()})
}
func (a *App) consumeRegistrationProof(token string, r *http.Request) bool {
	a.registrationMu.Lock()
	defer a.registrationMu.Unlock()
	c, ok := a.registrationChallenges[token]
	delete(a.registrationChallenges, token)
	return ok && c.verified && c.client == captchaClient(r) && time.Now().Before(c.expires)
}

// Generate a fresh courtyard illustration per request. Raster output avoids
// exposing gap coordinates through SVG paths or a reusable pristine asset.
func captchaImages(random *rng.Rand, target, y int) (string, string, error) {
	bg := image.NewNRGBA(image.Rect(0, 0, captchaWidth, captchaHeight))
	tint := random.IntN(22)
	for py := 0; py < captchaHeight; py++ {
		for px := 0; px < captchaWidth; px++ {
			noise := random.IntN(17)
			bg.SetNRGBA(px, py, color.NRGBA{uint8(29 + tint + py/8 + noise), uint8(64 + tint + py/5 + noise), uint8(66 + tint + py/7 + noise), 255})
		}
	}
	circle := func(cx, cy, r int, c color.NRGBA) {
		for dy := -r; dy <= r; dy++ {
			for dx := -r; dx <= r; dx++ {
				if dx*dx+dy*dy <= r*r {
					bg.SetNRGBA(cx+dx, cy+dy, c)
				}
			}
		}
	}
	line := func(x1, y1, x2, y2, width int, c color.NRGBA) {
		steps := max(abs(x2-x1), abs(y2-y1))
		for i := 0; i <= steps; i++ {
			x := x1 + (x2-x1)*i/max(1, steps)
			yy := y1 + (y2-y1)*i/max(1, steps)
			for dx := -width; dx <= width; dx++ {
				for dy := -width; dy <= width; dy++ {
					bg.SetNRGBA(x+dx, yy+dy, c)
				}
			}
		}
	}
	circle(205+random.IntN(80), 27+random.IntN(18), 17+random.IntN(7), color.NRGBA{239, 220, 171, 255})
	for layer := 0; layer < 3; layer++ {
		for px := 0; px < captchaWidth; px++ {
			ridge := 80 + layer*15 + int(13*math.Sin(float64(px)/float64(25+layer*12)+float64(tint+layer)))
			for py := ridge; py < captchaHeight; py++ {
				bg.SetNRGBA(px, py, color.NRGBA{uint8(36 + layer*10), uint8(75 + layer*9), uint8(72 + layer*8), 255})
			}
		}
	}
	ink := color.NRGBA{22, 48, 48, 255}
	gold := color.NRGBA{188, 170, 123, 255}
	roofX, roofY := 45+random.IntN(60), 78+random.IntN(23)
	line(roofX-42, roofY+10, roofX, roofY-12, 2, ink)
	line(roofX, roofY-12, roofX+46, roofY+10, 2, ink)
	line(roofX-42, roofY+10, roofX+46, roofY+10, 3, ink)
	for _, dx := range []int{-29, 0, 31} {
		line(roofX+dx, roofY+13, roofX+dx, 143, 2, ink)
	}
	line(0, 146, 320, 146, 1, gold)
	towerX := 180 + random.IntN(65)
	for py := 38; py < 145; py++ {
		half := int(5 + math.Pow(float64(py-83)/30, 2)*2)
		bg.SetNRGBA(towerX-half, py, gold)
		bg.SetNRGBA(towerX+half, py, gold)
		if py%11 == 0 {
			line(towerX-half, py, towerX+half, py, 0, gold)
		}
	}
	line(towerX, 17, towerX, 41, 0, gold)
	// Fine contours and varied texture keep the piece legible even over sky.
	for i := 0; i < 65; i++ {
		px, py := random.IntN(320), random.IntN(160)
		line(px, py, px+5+random.IntN(18), py-2, 0, color.NRGBA{uint8(90 + random.IntN(35)), uint8(132 + random.IntN(35)), uint8(126 + random.IntN(35)), 255})
	}
	mask := func(px, py int) bool {
		base := px >= 6 && px < 44 && py >= 10 && py < 48
		tab := (px-43)*(px-43)+(py-29)*(py-29) <= 64
		notch := (px-24)*(px-24)+(py-10)*(py-10) < 49
		return (base || tab) && !notch
	}
	piece := image.NewNRGBA(image.Rect(0, 0, captchaPiece, captchaPiece))
	for py := 0; py < captchaPiece; py++ {
		for px := 0; px < captchaPiece; px++ {
			if !mask(px, py) {
				continue
			}
			c := bg.NRGBAAt(target+px, y+py)
			edge := !mask(px-1, py) || !mask(px+1, py) || !mask(px, py-1) || !mask(px, py+1)
			piece.SetNRGBA(px, py, c)
			if edge {
				piece.SetNRGBA(px, py, color.NRGBA{249, 236, 200, 255})
				bg.SetNRGBA(target+px, y+py, color.NRGBA{200, 212, 187, 255})
			} else {
				bg.SetNRGBA(target+px, y+py, color.NRGBA{uint8(int(c.R) / 3), uint8(int(c.G) / 3), uint8(int(c.B) / 3), 255})
			}
		}
	}
	encode := func(img image.Image) (string, error) {
		var b bytes.Buffer
		if err := png.Encode(&b, img); err != nil {
			return "", err
		}
		return "data:image/png;base64," + base64.StdEncoding.EncodeToString(b.Bytes()), nil
	}
	background, err := encode(bg)
	if err != nil {
		return "", "", err
	}
	patch, err := encode(piece)
	return background, patch, err
}
func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}
