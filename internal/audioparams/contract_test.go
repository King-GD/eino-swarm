package audioparams

import (
	"encoding/json"
	"math"
	"reflect"
	"strings"
	"testing"
)

func pointer[T any](v T) *T { return &v }

const declaration = `{"version":1,"revision":"r1","status":"configured","parameters":{"voice":{"values":["a","b"],"default":"a","configurable":true},"response_format":{"values":["pcm"],"default":"pcm","configurable":false},"speed":{"min":0.1,"max":10,"default":1,"configurable":true},"instructions":{"max_characters":2,"default":"好","configurable":true},"max_input_characters":12}}`

func TestDefaultsRemainGatewayOwnedAndExplicitChoicesAreValidated(t *testing.T) {
	d := Parse([]byte(declaration), "speech")
	request, effective, err := Resolve(d, Overrides{})
	if err != nil || len(request) != 0 || effective["voice"] != "a" || effective["response_format"] != "pcm" || effective["speed"] != float64(1) || effective["instructions"] != "好" {
		t.Fatalf("%v %v %v", request, effective, err)
	}
	o := Overrides{Voice: pointer("b"), Speed: pointer(8.0), Instructions: pointer("😀好")}
	request, _, err = Resolve(d, o)
	if err != nil || request["voice"] != "b" || request["speed"] != 8.0 {
		t.Fatalf("%v %v", request, err)
	}
	for name, invalid := range map[string]Overrides{
		"unknown voice": {Voice: pointer("gone")}, "fixed even equal": {ResponseFormat: pointer("pcm")}, "unknown parameter": {Language: pointer("zh")},
		"too fast": {Speed: pointer(11.0)}, "not finite": {Speed: pointer(math.NaN())}, "infinite": {Speed: pointer(math.Inf(1))}, "too long": {Instructions: pointer("😀好啊")}, "invalid utf8": {Instructions: pointer(string([]byte{255}))}, "unsupported text": {Prompt: pointer("a")},
	} {
		t.Run(name, func(t *testing.T) {
			if _, _, err := Resolve(d, invalid); err == nil {
				t.Fatal("accepted invalid preference")
			}
		})
	}
	d.Parameters.Voice.Default = nil
	if _, _, err = Resolve(d, Overrides{}); err == nil {
		t.Fatal("required voice omitted")
	}
	d.Parameters.Speed.Configurable = false
	if _, _, err = Resolve(d, Overrides{Voice: pointer("a"), Speed: pointer(1.0)}); err == nil {
		t.Fatal("fixed speed override")
	}
	d.Parameters.Speed = nil
	if _, _, err = Resolve(d, Overrides{Voice: pointer("a"), Speed: pointer(1.0)}); err == nil {
		t.Fatal("unsupported speed")
	}
	d.Parameters.Instructions.Configurable = false
	if _, _, err = Resolve(d, Overrides{Voice: pointer("a"), Instructions: pointer("好")}); err == nil {
		t.Fatal("fixed text override")
	}
	if _, _, err = Resolve(Descriptor{}, Overrides{}); err == nil {
		t.Fatal("unsupported contract")
	}
}
func TestCatalogRefusesMalformedDeclarationsWithoutInventingOptions(t *testing.T) {
	for _, raw := range []string{`{`, strings.Replace(declaration, `"version":1`, `"version":2`, 1), strings.Replace(declaration, `"r1"`, `""`, 1), strings.Replace(declaration, `"configured"`, `"other"`, 1), strings.Replace(declaration, `["a","b"]`, `["a","a"]`, 1), strings.Replace(declaration, `["a","b"]`, `[]`, 1), strings.Replace(declaration, `["a","b"]`, `[""]`, 1), strings.Replace(declaration, `"default":"a"`, `"default":"c"`, 1), strings.Replace(declaration, `"max_characters":2`, `"max_characters":0`, 1), strings.Replace(declaration, `"max_characters":2`, `"max_characters":100001`, 1), strings.Replace(declaration, `"default":"好"`, `"default":"太长了"`, 1), strings.Replace(declaration, `"min":0.1`, `"min":0`, 1), strings.Replace(declaration, `"default":1`, `"default":11`, 1), strings.Replace(declaration, `"max_input_characters":12`, `"max_input_characters":-1`, 1), `{"version":1,"revision":"r","status":"configured","parameters":{"max_file_bytes":-1}}`, `{"version":1,"revision":"r","status":"configured","parameters":{"voice":{"values":["a"],"configurable":false}}}`, `{"version":1,"revision":"r","status":"configured"}`} {
		if d := Parse([]byte(raw), "speech"); d.Status != "unsupported" {
			t.Fatalf("accepted %s", raw)
		}
	}
	for _, status := range []string{"undeclared", "incompatible"} {
		d := Parse([]byte(`{"version":1,"revision":"r","status":"`+status+`","parameters":{}}`), "speech")
		if d.Status != status || d.Parameters != nil {
			t.Fatal(d)
		}
	}
	if Parse([]byte(declaration), "other").Status != "unsupported" {
		t.Fatal("unknown role")
	}
	tr := Parse([]byte(declaration), "transcription")
	if tr.Parameters.Voice != nil || tr.Parameters.Speed != nil || tr.Parameters.Instructions != nil || tr.Parameters.MaxInputCharacters != 0 {
		t.Fatal(tr)
	}
	raw := []byte(`{"data":[{"id":"speech","model_type":"speech","audio":` + declaration + `},{"id":"speech","model_type":"speech","audio":{}},{"id":"chat","model_type":"chat","audio":{}}]}`)
	if cat := ParseCatalog(raw); len(cat) != 1 || cat["speech"].Status != "configured" {
		t.Fatal(cat)
	}
	if single := ParseCatalog([]byte(`{"data":{"id":"voice","model_type":"speech","audio":` + declaration + `}}`)); single["voice"].Status != "configured" {
		t.Fatal(single)
	}
	if ParseCatalog([]byte(`{"data":"bad"}`)) != nil {
		t.Fatal("malformed single-item catalog")
	}
	if ParseCatalog([]byte("{")) != nil {
		t.Fatal("malformed catalog")
	}
}
func TestPreferencesAreDetachedAndRetainExplicitEmptyValues(t *testing.T) {
	o := Overrides{Voice: pointer("a"), ResponseFormat: pointer("pcm"), StreamFormat: pointer("sse"), Language: pointer("zh"), Speed: pointer(1.0), Instructions: pointer(""), Prompt: pointer("x")}
	original := map[string]Overrides{"scope": o}
	cloned := CloneOverrides(original)
	if !reflect.DeepEqual(cloned, original) || len(o.Values()) != 7 || CloneOverrides(nil) != nil {
		t.Fatal("clone or projection lost fields")
	}
	*cloned["scope"].Voice = "b"
	*cloned["scope"].Speed = 2
	if *original["scope"].Voice != "a" || *original["scope"].Speed != 1 {
		t.Fatal("preferences alias")
	}
	raw, _ := json.Marshal(cloned)
	if !strings.Contains(string(raw), `"instructions":""`) {
		t.Fatal("explicit empty lost")
	}
	if !reflect.DeepEqual(CloneOverrides(map[string]Overrides{"empty": {}})["empty"], Overrides{}) {
		t.Fatal("omission became explicit")
	}
}
