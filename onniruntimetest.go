package main

import (
	"encoding/json"
	"fmt"
	"log"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/sugarme/tokenizer"
	ort "github.com/yalue/onnxruntime_go"
)

type ModelPaths struct {
	Root      string // e.g., "models/bge-m3"
	ModelONNX string // Root/model.onnx
	TokJSON   string // Root/tokenizer.json
}

// ===== util =====

func must(err error) {
	if err != nil {
		log.Fatal(err)
	}
}

func exeDir() string {
	p, err := os.Executable()
	must(err)
	return filepath.Dir(p)
}

func cosine(a, b []float32) float64 {
	var dot, na, nb float64
	for i := range a {
		fa := float64(a[i])
		fb := float64(b[i])
		dot += fa * fb
		na += fa * fa
		nb += fb * fb
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
}

func l2norm(v []float32) {
	var s float64
	for _, x := range v {
		s += float64(x) * float64(x)
	}
	n := float32(math.Sqrt(s) + 1e-12)
	for i := range v {
		v[i] /= n
	}
}

// ===== ORT init =====

func initORT() {
	// あなたの配置に合わせた DLL パス
	lib := filepath.Join(exeDir(), "onnixruntime-win", "lib",
		map[string]string{
			"windows": "onnxruntime.dll",
			"linux":   "libonnxruntime.so",
			"darwin":  "libonnxruntime.dylib",
		}[runtime.GOOS],
	)

	// Windows は上の Join だと ...\lib\onnxruntime.dll\ になり得るので補正
	if runtime.GOOS == "windows" {
		lib = filepath.Join(exeDir(), "onnixruntime-win", "lib", "onnxruntime.dll")
	}

	if _, err := os.Stat(lib); err != nil {
		log.Fatalf("ONNX Runtime が見つかりません: %s", lib)
	}
	must(ort.SetSharedLibraryPath(lib))
	must(ort.InitializeEnvironment())
}

func finiORT() { ort.DestroyEnvironment() }

// ===== tokenizer =====

func loadTokenizer(tokPath string) *tokenizer.Tokenizer {
	tk, err := tokenizer.FromFile(tokPath)
	must(err)
	return tk
}

// seq を maxLen に合わせてトリム（後方切り）＆ attention mask を作る
func encodeIDs(tk *tokenizer.Tokenizer, text string, maxLen int) (ids []int64, attn []int64) {
	enc, err := tk.EncodeSingle(text)
	must(err)
	raw := enc.IDs
	if len(raw) > maxLen {
		raw = raw[:maxLen]
	}
	ids = make([]int64, len(raw))
	attn = make([]int64, len(raw))
	for i, v := range raw {
		ids[i] = int64(v)
		attn[i] = 1
	}
	return
}

// ===== embedding =====

func tryRun(session *ort.Session, inputs map[string]*ort.Tensor, candOutputs []string) (*ort.Tensor, string) {
	for _, name := range candOutputs {
		outs, err := session.Run(inputs, []string{name})
		if err == nil && len(outs) == 1 && outs[0] != nil {
			return outs[0], name
		}
	}
	return nil, ""
}

func embedOne(session *ort.Session, ids, attn []int64) []float32 {
	batch := int64(1)
	seq := int64(len(ids))
	// 入力テンソル
	tInput, err := ort.NewTensor(ort.NewShape(batch, seq), ids)
	must(err)
	defer tInput.DestroyTensor()
	tMask, err := ort.NewTensor(ort.NewShape(batch, seq), attn)
	must(err)
	defer tMask.DestroyTensor()
	inputs := map[string]*ort.Tensor{
		"input_ids":      tInput,
		"attention_mask": tMask,
	}
	// 出力候補：モデルにより名称が違う
	candidates := []string{
		"last_hidden_state",  // BERT系一般
		"sentence_embedding", // 一部のOptimum出力
		"pooler_output",      // pooled
		"output",             // 稀にこの名前
	}

	out, used := tryRun(session, inputs, candidates)
	if out == nil {
		log.Fatalf("推論出力名が見つかりません。候補: %v", candidates)
	}
	defer out.DestroyTensor()

	// 形状と生データ
	dims := out.GetShape() // 通常 [1, seq, hidden] or [1, hidden]
	raw := out.GetTensorData().([]float32)

	// もし [1, hidden]（pooler系）ならそのまま返す
	if len(dims) == 2 && dims[0] == 1 {
		vec := make([]float32, int(dims[1]))
		copy(vec, raw[:len(vec)])
		l2norm(vec)
		return vec
	}

	// 通常: [1, seq, hidden] を Mean Pooling（attention_maskでマスク）
	if len(dims) != 3 || dims[0] != 1 {
		log.Fatalf("予期しない出力形状 %v（使用出力: %s）", dims, used)
	}
	seqLen := int(dims[1])
	hidden := int(dims[2])
	if seqLen != len(ids) {
		// モデル側でpad/切り詰めが入ることもあるので、短い方に合わせる
		seqLen = min(seqLen, len(ids))
	}
	pool := make([]float32, hidden)
	valid := float32(0)
	for t := 0; t < seqLen; t++ {
		if attn[t] == 0 {
			continue
		}
		valid += 1
		base := t * hidden
		for h := 0; h < hidden; h++ {
			pool[h] += raw[base+h]
		}
	}
	if valid > 0 {
		inv := 1 / valid
		for h := 0; h < hidden; h++ {
			pool[h] *= inv
		}
	}
	l2norm(pool)
	return pool
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// ===== main =====

func main() {
	// 1) ORT 初期化
	initORT()
	defer finiORT()

	// 2) モデルとトークナイザのパス
	//    お好みで "bge-m3" を "e5-small" や "xenova" に差し替えてOK
	model := ModelPaths{
		Root:      filepath.Join(exeDir(), "models", "bge-m3"),
		ModelONNX: filepath.Join(exeDir(), "models", "bge-m3", "model.onnx"),
		TokJSON:   filepath.Join(exeDir(), "models", "bge-m3", "tokenizer.json"),
	}
	if _, err := os.Stat(model.ModelONNX); err != nil {
		log.Fatalf("model.onnx が見つかりません: %s", model.ModelONNX)
	}
	if _, err := os.Stat(model.TokJSON); err != nil {
		log.Fatalf("tokenizer.json が見つかりません: %s", model.TokJSON)
	}

	// 3) トークナイザ
	tk := loadTokenizer(model.TokJSON)

	// 4) セッション作成（必要に応じて SessionOptions を設定可能）
	session, err := ort.NewSession(model.ModelONNX)
	must(err)
	defer session.DestroySession()

	// 5) 日本語テキストでテスト
	texts := []string{
		"渋谷で静かなWi-Fiカフェを探しています。",
		"ノートPC作業に向いた落ち着いた喫茶店を知りたい。",
		"今日はスノーボードのレンタルが安い店を調べたい。",
	}
	const maxLen = 512

	embs := make([][]float32, 0, len(texts))
	for _, t := range texts {
		ids, attn := encodeIDs(tk, t, maxLen)
		vec := embedOne(session, ids, attn)
		embs = append(embs, vec)
		fmt.Printf("OK: %s\n", preview(t))
	}

	// 6) コサイン類似度を出力
	fmt.Println("\n== Cosine Similarity ==")
	for i := range texts {
		for j := i + 1; j < len(texts); j++ {
			s := cosine(embs[i], embs[j])
			fmt.Printf("(%d,%d) %s  vs  %s  => %.3f\n",
				i, j, preview(texts[i]), preview(texts[j]), s)
		}
	}
}

func preview(s string) string {
	r := []rune(s)
	if len(r) > 22 {
		r = r[:22]
	}
	out, _ := json.Marshal(string(r))
	return strings.Trim(string(out), `"`)
}
