// Transparent reverse-proxy to the Cloud Run ingest service.
// Method, path, query, headers (including Authorization) and body are forwarded
// as-is. Host is taken from ORIGIN so the Cloud Run origin hostname is forwarded.
export default {
  async fetch(request, env) {
    const origin = new URL(env.ORIGIN);
    const url = new URL(request.url);
    url.protocol = origin.protocol;
    url.hostname = origin.hostname;
    url.port = origin.port;

    try {
      return await fetch(new Request(url, request), { redirect: "manual" });
    } catch (err) {
      return new Response("upstream unavailable", { status: 502 });
    }
  },
};
