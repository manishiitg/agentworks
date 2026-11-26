package handlers

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/manishiitg/coding-agent-loop/workspace/browserconfig"
	"github.com/manishiitg/coding-agent-loop/workspace/models"
	"github.com/manishiitg/coding-agent-loop/workspace/security"
)

// PLAT-746-1005: the agent service admits the source browser and destination
// chat. This service-only operation reads that existing daemon; it cannot
// navigate, select a different tab, run caller JS or launch another browser.
func BrowserChatContext(c *gin.Context) {
	var in models.BrowserChatContextRequest
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 2048)
	if c.ShouldBindJSON(&in) != nil || (in.X == nil) != (in.Y == nil) || len(in.DocumentToken) > 64 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Pick an element in the current page."})
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 12*time.Second)
	defer cancel()
	out, err := readBrowserChatContext(ctx, c.Param("session"), in)
	if err != nil {
		// Page text, URLs and daemon error details must not enter access logs.
		c.JSON(http.StatusConflict, gin.H{"error": "The page changed or this element could not be captured. Pick it again."})
		return
	}
	c.JSON(http.StatusOK, out)
}

// The page supplies data, never instructions or an executable script. Avoid
// form values, editable regions, whole-document dumps and unbounded walks.
const browserContextScript = `([x,y]) => {
 const doc = [location.href,performance.timeOrigin,innerWidth,innerHeight,scrollX,scrollY];
 const out = {document:JSON.stringify(doc),url:location.href,width:innerWidth,height:innerHeight,rect:{x:0,y:0,width:0,height:0}};
 if (x === null) return out;
 const el = document.elementFromPoint(x,y);
 if (!el || ['HTML','BODY','IFRAME'].includes(el.tagName) || el.closest('input,textarea,[contenteditable]:not([contenteditable="false"])')) return null;
 const r = el.getBoundingClientRect();
 if (r.width<=0 || r.height<=0) return null;
 const clipped={x:Math.max(0,r.x),y:Math.max(0,r.y),width:Math.min(innerWidth,r.right)-Math.max(0,r.x),height:Math.min(innerHeight,r.bottom)-Math.max(0,r.y)};
 if(clipped.width<=0 || clipped.height<=0) return null;
 let part=el, path=[];
 for(let n=0;part && n<8;n++,part=part.parentElement) {
   if(part.id && part.id.length<=128) {
     const id='#'+CSS.escape(part.id);
     if(document.querySelectorAll(id).length===1) {path.unshift(id); break;}
   }
   const tag=part.tagName.toLowerCase();
   let index=1, scanned=0;
   for(let prev=part.previousElementSibling;prev;prev=prev.previousElementSibling) {
     if(++scanned>256) return null;
     if(prev.tagName===part.tagName) index++;
   }
   path.unshift(tag+':nth-of-type('+index+')');
 }
 const selector=path.join(' > ');
 if(selector.length>1024 || document.querySelectorAll(selector).length!==1 || document.querySelector(selector)!==el) return null;
 const walker=document.createTreeWalker(el,NodeFilter.SHOW_TEXT);
 let text='', node;
 for(let n=0;n<256 && text.length<4096 && (node=walker.nextNode());n++) {
   if(!node.parentElement?.closest('input,textarea,script,style,[contenteditable]:not([contenteditable="false"])')) text+=node.textContent.slice(0,4096-text.length)+' ';
 }
 return {...out,selector,text:text.trim().slice(0,4096),rect:clipped};
}`

func browserContextMetadata(ctx context.Context, dir, session string, x, y *float64) (models.BrowserChatContext, error) {
	point, _ := json.Marshal([]*float64{x, y})
	id := uuid.NewString()
	out, err := existingBrowserCommand(ctx, dir, session, map[string]any{"id": id, "action": "evaluate", "script": "(" + browserContextScript + ")(" + string(point) + ")"})
	if err != nil {
		return models.BrowserChatContext{}, err
	}
	var response struct {
		Data struct {
			Origin string `json:"origin"`
			Result *struct {
				models.BrowserChatContext
				Document string `json:"document"`
			} `json:"result"`
		} `json:"data"`
	}
	if json.Unmarshal(out, &response) != nil || response.Data.Result == nil {
		return models.BrowserChatContext{}, fmt.Errorf("element unavailable")
	}
	result := response.Data.Result
	if result.Document == "" || response.Data.Origin != result.URL || len(result.Document) > 16384 || len(result.URL) > 8192 || len(result.Selector) > 1024 || len(result.Text) > 16384 || !contextFinite(result.Width, result.Height, result.Rect.X, result.Rect.Y, result.Rect.Width, result.Rect.Height) || result.Width < 1 || result.Height < 1 || result.Width > 4096 || result.Height > 4096 {
		return models.BrowserChatContext{}, fmt.Errorf("invalid page metadata")
	}
	token := sha256.Sum256([]byte(response.Data.Origin + "\x00" + result.Document))
	result.DocumentToken = hex.EncodeToString(token[:])
	return result.BrowserChatContext, nil
}

func contextFinite(values ...float64) bool {
	for _, v := range values {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return false
		}
	}
	return true
}

func readBrowserChatContext(ctx context.Context, session string, in models.BrowserChatContextRequest) (models.BrowserChatContext, error) {
	if !browserconfig.IsUserSession(session) || (in.X == nil) != (in.Y == nil) {
		return models.BrowserChatContext{}, fmt.Errorf("managed browser required")
	}
	_, dir, err := browserLiveEndpoint(session)
	if err != nil {
		return models.BrowserChatContext{}, err
	}
	if in.X != nil && (!contextFinite(*in.X, *in.Y) || *in.X < 0 || *in.Y < 0 || *in.X > 4096 || *in.Y > 4096 || len(in.DocumentToken) != 64) {
		return models.BrowserChatContext{}, fmt.Errorf("invalid pick")
	}
	before, err := browserContextMetadata(ctx, dir, session, in.X, in.Y)
	if err != nil || in.X == nil {
		return before, err
	}
	if before.DocumentToken != in.DocumentToken || before.Selector == "" || *in.X >= before.Width || *in.Y >= before.Height || before.Rect.X < 0 || before.Rect.Y < 0 || before.Rect.Width <= 0 || before.Rect.Height <= 0 || before.Rect.X+before.Rect.Width > before.Width || before.Rect.Y+before.Rect.Height > before.Height {
		return models.BrowserChatContext{}, fmt.Errorf("stale pick")
	}
	stage := filepath.Join(browserconfig.ArtifactDirForSession(session), uuid.NewString()+".png")
	if err = security.PrepareBrowserArtifactStaging(stage, session); err != nil {
		return models.BrowserChatContext{}, err
	}
	defer os.Remove(stage)
	// A bounded viewport capture is cropped here, rather than asking an
	// untrusted page's selector to generate an arbitrarily large screenshot.
	_, err = existingBrowserCommand(ctx, dir, session, map[string]any{"id": uuid.NewString(), "action": "screenshot", "path": stage, "fullPage": false, "format": "png"})
	if err != nil {
		return models.BrowserChatContext{}, err
	}
	after, err := browserContextMetadata(ctx, dir, session, in.X, in.Y)
	if err != nil || before.DocumentToken != after.DocumentToken || before.Selector != after.Selector || before.Text != after.Text || before.Rect != after.Rect {
		return models.BrowserChatContext{}, fmt.Errorf("page changed during capture")
	}
	before.ImageBase64, err = browserContextThumbnail(stage, before)
	return before, err
}

func browserContextThumbnail(path string, meta models.BrowserChatContext) (string, error) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > 8<<20 {
		return "", fmt.Errorf("invalid screenshot")
	}
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return "", fmt.Errorf("screenshot changed")
	}
	data, err := io.ReadAll(io.LimitReader(file, (8<<20)+1))
	if err != nil || len(data) > 8<<20 {
		return "", fmt.Errorf("screenshot too large")
	}
	cfg, err := png.DecodeConfig(bytes.NewReader(data))
	if err != nil || cfg.Width < 1 || cfg.Height < 1 || cfg.Width > 4096 || cfg.Height > 4096 {
		return "", fmt.Errorf("invalid screenshot dimensions")
	}
	source, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		return "", fmt.Errorf("invalid PNG")
	}
	sx, sy := float64(cfg.Width)/meta.Width, float64(cfg.Height)/meta.Height
	rect := image.Rect(int(math.Floor(meta.Rect.X*sx)), int(math.Floor(meta.Rect.Y*sy)), int(math.Ceil((meta.Rect.X+meta.Rect.Width)*sx)), int(math.Ceil((meta.Rect.Y+meta.Rect.Height)*sy)))
	if rect.Empty() || !rect.In(source.Bounds()) {
		return "", fmt.Errorf("element outside screenshot")
	}
	scale := math.Min(1, math.Min(512/float64(rect.Dx()), 512/float64(rect.Dy())))
	thumb := image.NewNRGBA(image.Rect(0, 0, max(1, int(float64(rect.Dx())*scale)), max(1, int(float64(rect.Dy())*scale))))
	for y := 0; y < thumb.Bounds().Dy(); y++ {
		for x := 0; x < thumb.Bounds().Dx(); x++ {
			thumb.SetNRGBA(x, y, color.NRGBAModel.Convert(source.At(rect.Min.X+int(float64(x)/scale), rect.Min.Y+int(float64(y)/scale))).(color.NRGBA))
		}
	}
	var encoded bytes.Buffer
	if err = png.Encode(&encoded, thumb); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(encoded.Bytes()), nil
}
