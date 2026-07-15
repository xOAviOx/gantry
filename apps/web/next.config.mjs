/** @type {import('next').NextConfig} */
const nextConfig = {
  reactStrictMode: true,
  // Requests arrive proxied by Caddy with Host: paas.localhost. Allow that
  // origin so Next dev (HMR / server actions cross-origin checks) is happy.
  allowedDevOrigins: ["paas.localhost"],
};

export default nextConfig;
