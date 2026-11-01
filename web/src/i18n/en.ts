// All UI strings live here (English). Add tr.ts later for Turkish.
export const en = {
  appName: 'HomeStreaming',
  loading: 'Loading…',
  retry: 'Retry',
  cancel: 'Cancel',
  save: 'Save',
  saved: 'Saved',
  close: 'Close',
  back: 'Back',
  next: 'Next',
  done: 'Done',
  delete: 'Delete',
  edit: 'Edit',
  error: 'Something went wrong',
  offline: 'Cannot reach the server. Is it running?',

  navHome: 'Home',
  navSearch: 'Search',
  navSettings: 'Settings',
  navAdmin: 'Admin',
  signOut: 'Sign out',

  // Setup
  setupTitle: 'Welcome to HomeStreaming',
  setupFfmpeg: 'Step 1 · ffmpeg',
  setupFfmpegOk: 'ffmpeg found: {path} (version {version})',
  setupFfmpegMissing: 'ffmpeg was not found. Playback and thumbnails need it.',
  setupFfmpegHint: 'Install it on the server PC with:',
  setupFfmpegPath: 'Or enter the full path to ffmpeg.exe',
  setupCheckAgain: 'Check again',
  setupAccount: 'Step 2 · Your account',
  setupLibrary: 'Step 3 · Your media folder',
  setupScan: 'Step 4 · Scanning',
  setupScanDone: 'Your library is ready.',
  setupGoHome: 'Go to Home',
  setupSkipLibrary: 'Skip for now',
  username: 'Username',
  password: 'Password',
  passwordConfirm: 'Confirm password',
  passwordMismatch: 'Passwords do not match',
  passwordShort: 'At least 8 characters',
  serverName: 'Server name',
  createAccount: 'Create account',

  // Login
  loginTitle: 'Sign in',
  loginButton: 'Sign in',
  loginInvalid: 'Wrong username or password.',
  loginLocked: 'Too many attempts. Try again in {seconds} s.',
  loginRateLimited: 'Too many attempts. Wait a minute and try again.',

  // Home
  continueWatching: 'Continue Watching',
  nextUp: 'Next Up',
  recentlyAdded: 'Recently Added',
  movies: 'Movies',
  shows: 'Shows',
  seeAll: 'See all',
  emptyHome: 'No media yet. Add a library in Admin.',

  // Library
  sortTitle: 'Title',
  sortAdded: 'Date added',
  sortYear: 'Year',
  searchPlaceholder: 'Search movies and shows',
  noResults: 'Nothing found.',
  processing: 'processing',

  // Item
  play: 'Play',
  resume: 'Resume from {time}',
  playFromStart: 'Play from beginning',
  markPlayed: 'Mark played',
  markUnplayed: 'Mark unplayed',
  audio: 'Audio',
  subtitles: 'Subtitles',
  subtitlesOff: 'Off',
  season: 'Season {n}',
  specials: 'Specials',
  episodeShort: 'S{s}·E{e}',
  episodes: '{n} episodes',
  unplayed: '{n} unplayed',
  playNext: 'Play {label}',
  notReady: 'This file is still being analysed.',
  missingFile: 'File missing',

  // Player
  quality: 'Quality',
  qualityAuto: 'Auto',
  qualityAutoCap: 'Auto ({mbps} Mbps, {cls})',
  forceTranscode: 'Always transcode',
  whyTranscoding: 'Why: {reasons}',
  methodDirect: 'Direct play',
  methodRemux: 'Direct stream (remux)',
  methodTranscode: 'Transcoding',
  upNext: 'Up next',
  playingIn: 'Playing in {n} s',
  playNow: 'Play now',
  playbackError: 'Playback failed.',
  retryingTranscode: 'Retrying with transcoding…',
  busy: 'The server is busy transcoding other streams.',
  ffmpegMissing: 'The server cannot play this: ffmpeg is missing.',
  fullscreen: 'Fullscreen',
  rewind: 'Back 10 s',
  forward: 'Forward 10 s',

  // Settings
  settingsTitle: 'Settings',
  account: 'Account',
  changePassword: 'Change password',
  currentPassword: 'Current password',
  newPassword: 'New password',
  passwordChanged: 'Password changed. Other devices were signed out.',
  preferences: 'Preferences',
  audioLanguage: 'Preferred audio language',
  subtitleLanguage: 'Preferred subtitle language',
  subtitleMode: 'Subtitles',
  subModeOff: 'Off',
  subModePreferred: 'When available in preferred language',
  subModeAlways: 'Always',
  playback: 'Playback on this device',
  maxBitrate: 'Maximum quality',
  engine: 'Player engine',
  engineAuto: 'Automatic',
  devices: 'Signed-in devices',
  thisDevice: 'this device',
  revoke: 'Sign out',
  tvMode: 'TV mode (remote-control navigation)',
  none: 'None',

  // Admin
  adminTitle: 'Admin',
  status: 'Server status',
  libraries: 'Libraries',
  addLibrary: 'Add library',
  libraryName: 'Name',
  libraryType: 'Type',
  typeMixed: 'Movies & shows (mixed)',
  typeMovies: 'Movies',
  typeShows: 'Shows',
  folders: 'Folders',
  browse: 'Browse…',
  chooseFolder: 'Choose this folder',
  scan: 'Scan',
  scanFull: 'Full rescan',
  clean: 'Remove missing',
  scanning: 'Scanning ({phase} {done}/{total})',
  lastScan: 'Last scan: {when}',
  serverSettings: 'Server settings',
  remoteCap: 'Remote bitrate cap (kbps, 0 = unlimited)',
  lanCap: 'LAN bitrate cap (kbps, 0 = unlimited)',
  tailnetIsLan: 'Treat Tailscale clients as LAN',
  maxSessions: 'Max simultaneous transcodes',
  preset: 'Software encoder preset',
  scanInterval: 'Rescan every (hours)',
  playedThreshold: 'Mark played at (%)',
  audioCodec: 'Audio transcode codec',
  problems: 'Problems',
  noProblems: 'No problems.',
  activeSessions: 'Now playing',
  noSessions: 'Nothing is playing.',
  tasks: 'Tasks',
  hwaccel: 'Hardware transcoding',
  selfTests: 'Self-tests',
  confirmDelete: 'Delete library "{name}"? Watched progress for its items is removed.',
} as const;

export type Key = keyof typeof en;

export function episodesLabel(n: number): string {
  return n === 1 ? '1 episode' : t('episodes', { n });
}

export function t(key: Key, params?: Record<string, string | number>): string {
  let s: string = en[key];
  if (params) {
    for (const k of Object.keys(params)) {
      s = s.split('{' + k + '}').join(String(params[k]));
    }
  }
  return s;
}
