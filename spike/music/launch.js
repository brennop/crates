// launch: start Music if it is not running.
// argv: none. Result: {"running": true}
const NEEDS_RUNNING = false;
function main(argv, music) {
  if (!music.running()) music.launch();
  return { running: music.running() };
}
