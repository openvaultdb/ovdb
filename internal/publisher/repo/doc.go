// Package repo judges a repository as far as the presence of files goes: OVDB.md and
// the manifests it lists, and the files an own-form manifest names, are tracked regular
// files of the commit at HEAD, as committed and never from the working tree; every
// manifest is judged with package manifest; publisher.repository is the --repository
// given; and what the model file and the meaning file say agrees with the manifest (the module, the entities and the recordsets, the id, the
// license and the models: entry), so that nothing the Chinook checker refuses about a whole repository is accepted. It reads through a Reader, with an in-memory one (Memory) and one over git (Git,
// with ExecRunner). See the README for the limits, the rules made and the rules left.
package repo
