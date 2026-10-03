// The email pipeline's bundler entry. The templates render in-process
// inside the plugin (tsx loader + react-email), never through the bundle;
// the build only exists so the plugin's closeBundle runs.
export {}
