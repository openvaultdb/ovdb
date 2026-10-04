// The references that the generators of the parity goldens read, and where they live: the one place to change when a
// reference moves to another organisation or to another commit. Each generator (rules/testdata/reference and
// manifest/testdata/reference) imports this file; nothing else in the repository names a reference's location.
//
// A reference is fetched at exactly its commit, by `remoteUrl`, into a temporary directory.
export const references = {
  directory: { repository: 'openvaultdb/directory', commit: 'e8db5488db31d3f63865e404acef487c33cf35df' },
  chinookdb: { repository: 'datatug/chinookdb', commit: '79e7bb0b1d6f0666dce465874990dec64348331f' },
};

// The URL a reference is fetched from.
export const remoteUrl = (name) => `https://github.com/${references[name].repository}.git`;
