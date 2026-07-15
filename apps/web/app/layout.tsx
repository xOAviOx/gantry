import type { Metadata } from "next";
import "./globals.css";
import Providers from "./providers";

export const metadata: Metadata = {
  title: "Gantry",
  description: "Single-node mini-PaaS — git push to live URL",
};

export default function RootLayout({
  children,
}: {
  children: React.ReactNode;
}) {
  return (
    <html lang="en">
      <body className="min-h-screen bg-ink font-mono text-[#e6edf3] antialiased">
        <Providers>{children}</Providers>
      </body>
    </html>
  );
}
