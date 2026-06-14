// CUE schema for the `candy` kind. #Candy validates ONE candy entity (the value
// under `candy:` in a kind-keyed candy charly.yml). OPEN (...) for now; the key
// invariants are constrained. Shared #Step lives in _common.cue.

#Candy: {
	version:     string & =~"^[0-9]{4}\\.[0-9]{1,3}\\.[0-9]{3,4}$"
	name:        string & =~"^[a-z0-9]+(-[a-z0-9]+)*$"
	description: string & !=""
	status:      *"testing" | "working" | "broken"
	plan: [...#Step]
	...
}
