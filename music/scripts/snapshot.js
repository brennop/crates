// snapshot: every file track's persistent ID and location, then every user
// playlist with its flags and track IDs. Bulk property reads only: one Apple
// Event per property, not per track.
// argv: none.
// Result: {"tracks": [{"id","location"}],
//          "playlists": [{"id","name","smart","folder","special","parent","trackIDs"}],
//          "ms": {"tracks": N, "playlists": N}}
function main(argv, music) {
  const t0 = Date.now();
  const ft = music.libraryPlaylists[0].fileTracks;
  const ids = ft.persistentID();
  const locs = ft.location();
  const tracks = ids.map((id, i) => ({ id: id, location: posixOrNull(locs[i]) }));
  const t1 = Date.now();

  const ups = music.userPlaylists;
  const pids = ups.persistentID();
  const names = ups.name();
  const smart = ups.smart();
  const special = ups.specialKind();
  const playlists = pids.map((id, i) => (
    { id: id, name: names[i], smart: smart[i], folder: special[i] === 'folder', special: special[i] }));
  for (let i = 0; i < playlists.length; i++) {
    const p = playlists[i];
    const ref = ups[i];
    let parent = null;
    try { parent = ref.parent.persistentID(); } catch (e) { parent = null; }
    p.parent = parent;
    p.trackIDs = p.folder ? [] : playlistTrackIDs(ref);
  }
  const t2 = Date.now();
  return { tracks: tracks, playlists: playlists, ms: { tracks: t1 - t0, playlists: t2 - t1 } };
}
