// hls.js ships no typings for the light build; it has the same API.
declare module 'hls.js/light' {
  import Hls from 'hls.js';
  export default Hls;
  export * from 'hls.js';
}
