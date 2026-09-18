import Link from 'next/link';
import type { ReactNode } from 'react';
import LiveNetworkStats from './live-stats';

const features: { title: string; description: string; icon: ReactNode }[] = [
  {
    title: 'Peer-to-Peer Orchestration',
    description:
      'Decentralized resource scheduling over a libp2p mesh network. No single point of failure.',
    icon: (
      <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" className="size-6">
        <path strokeLinecap="round" strokeLinejoin="round" d="M7.5 21 3 16.5m0 0L7.5 12M3 16.5h13.5m0-13.5L21 7.5m0 0L16.5 12M21 7.5H7.5" />
      </svg>
    ),
  },
  {
    title: 'GPU-Native',
    description:
      'Built for GPU workloads. Auto-detects hardware via nvidia-smi and integrates with Slurm clusters.',
    icon: (
      <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" className="size-6">
        <path strokeLinecap="round" strokeLinejoin="round" d="M8.25 3v1.5M4.5 8.25H3m18 0h-1.5M4.5 12H3m18 0h-1.5m-15 3.75H3m18 0h-1.5M8.25 19.5V21M12 3v1.5m0 15V21m3.75-18v1.5m0 15V21m-9-1.5h10.5a2.25 2.25 0 0 0 2.25-2.25V6.75a2.25 2.25 0 0 0-2.25-2.25H6.75A2.25 2.25 0 0 0 4.5 6.75v10.5a2.25 2.25 0 0 0 2.25 2.25Z" />
      </svg>
    ),
  },
  {
    title: 'CRDT Consensus',
    description:
      'Conflict-free replicated state with no central coordinator. Gossip-based propagation across all peers.',
    icon: (
      <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" className="size-6">
        <path strokeLinecap="round" strokeLinejoin="round" d="M20.25 6.375c0 2.278-3.694 4.125-8.25 4.125S3.75 8.653 3.75 6.375m16.5 0c0-2.278-3.694-4.125-8.25-4.125S3.75 4.097 3.75 6.375m16.5 0v11.25c0 2.278-3.694 4.125-8.25 4.125s-8.25-1.847-8.25-4.125V6.375m16.5 0v3.75m-16.5-3.75v3.75m16.5 0v3.75C20.25 16.153 16.556 18 12 18s-8.25-1.847-8.25-4.125v-3.75m16.5 0c0 2.278-3.694 4.125-8.25 4.125s-8.25-1.847-8.25-4.125" />
      </svg>
    ),
  },
  {
    title: 'Smart Routing',
    description:
      'Three-tier request matching with automatic fallback. Route by model, capability, or catch-all.',
    icon: (
      <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" className="size-6">
        <path strokeLinecap="round" strokeLinejoin="round" d="M7.5 21 3 16.5m0 0L7.5 12M3 16.5h13.5m0-13.5L21 7.5m0 0L16.5 12M21 7.5H7.5" />
        <path strokeLinecap="round" strokeLinejoin="round" d="M3.75 12h16.5" />
      </svg>
    ),
  },
  {
    title: 'OpenAI-Compatible API',
    description:
      'Drop-in replacement for OpenAI endpoints. Point your existing apps at an OpenTela cluster.',
    icon: (
      <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" className="size-6">
        <path strokeLinecap="round" strokeLinejoin="round" d="M17.25 6.75 22.5 12l-5.25 5.25m-10.5 0L1.5 12l5.25-5.25m7.5-3-4.5 16.5" />
      </svg>
    ),
  },
];

const news: { tag: string; title: string; description: string; href: string }[] = [
  {
    tag: 'Deployment',
    title: 'How SwissAI Uses OpenTela for Scalable LLM Serving',
    description:
      'How the Swiss AI Initiative leverages OpenTela to serve LLMs on HPC clusters, enabling researchers to access powerful models seamlessly.',
    href: '/docs/blog/swissai',
  },
  {
    tag: 'Research',
    title: "Reproducing the Trace Analysis Figures from our OSDI '26 Paper",
    description:
      "Artifact guide for the trace-analysis figures in our OSDI '26 paper — find the SwissAI serving trace and regenerate the plots from the OpenTela repository.",
    href: '/docs/blog/osdi26-trace-artifact',
  },
];

const GitHubIcon = (
  <svg viewBox="0 0 24 24" fill="currentColor" className="size-5">
    <path d="M12 .5C5.37.5 0 5.78 0 12.29c0 5.21 3.44 9.63 8.21 11.19.6.11.82-.25.82-.56 0-.28-.01-1.02-.02-2-3.34.71-4.04-1.58-4.04-1.58-.55-1.36-1.34-1.73-1.34-1.73-1.09-.73.08-.71.08-.71 1.2.08 1.84 1.21 1.84 1.21 1.07 1.79 2.81 1.27 3.49.97.11-.76.42-1.27.76-1.56-2.67-.3-5.47-1.31-5.47-5.81 0-1.28.47-2.33 1.24-3.15-.12-.3-.54-1.51.12-3.15 0 0 1.01-.32 3.3 1.2a11.6 11.6 0 0 1 3-.4c1.02 0 2.05.13 3 .4 2.29-1.52 3.3-1.2 3.3-1.2.66 1.64.24 2.85.12 3.15.77.82 1.24 1.87 1.24 3.15 0 4.51-2.81 5.5-5.49 5.79.43.36.81 1.08.81 2.18 0 1.58-.01 2.85-.01 3.24 0 .31.21.68.83.56C20.56 21.91 24 17.5 24 12.29 24 5.78 18.63.5 12 .5Z" />
  </svg>
);

const community: { label: string; description: string; href: string; external?: boolean; icon: ReactNode }[] = [
  {
    label: 'GitHub',
    description: 'Source, issues & releases',
    href: 'https://github.com/eth-easl/OpenTela',
    external: true,
    icon: GitHubIcon,
  },
  {
    label: 'Documentation',
    description: 'Guides, tutorials & internals',
    href: '/docs',
    icon: (
      <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" className="size-5">
        <path strokeLinecap="round" strokeLinejoin="round" d="M12 6.042A8.967 8.967 0 0 0 6 3.75c-1.052 0-2.062.18-3 .512v14.25A8.987 8.987 0 0 1 6 18c2.305 0 4.408.867 6 2.292m0-14.25a8.966 8.966 0 0 1 6-2.292c1.052 0 2.062.18 3 .512v14.25A8.987 8.987 0 0 0 18 18a8.967 8.967 0 0 0-6 2.292m0-14.25v14.25" />
      </svg>
    ),
  },
  {
    label: 'Observatory',
    description: 'Measured GPU throughput on the mesh',
    href: '/observatory',
    icon: (
      <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" className="size-5">
        <path strokeLinecap="round" strokeLinejoin="round" d="M3 3v13.5A2.5 2.5 0 0 0 5.5 19H19" />
        <path strokeLinecap="round" strokeLinejoin="round" d="M7 14l3-4 3.5 3 4.5-6" />
      </svg>
    ),
  },
  {
    label: 'Account',
    description: 'API keys & wallet dashboard',
    href: 'https://cloud.opentela.ai/account',
    external: true,
    icon: (
      <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" className="size-5">
        <path strokeLinecap="round" strokeLinejoin="round" d="M21 12.75V9.75A2.25 2.25 0 0 0 18.75 7.5H5.25A2.25 2.25 0 0 0 3 9.75v8.25A2.25 2.25 0 0 0 5.25 20.25h13.5A2.25 2.25 0 0 0 21 18v-3" />
        <path strokeLinecap="round" strokeLinejoin="round" d="M16.5 12.75h4.125c.483 0 .875.392.875.875v1.75a.875.875 0 0 1-.875.875H16.5a1.75 1.75 0 1 1 0-3.5Z" />
        <path strokeLinecap="round" strokeLinejoin="round" d="M6.75 7.5V5.625A1.875 1.875 0 0 1 8.625 3.75h8.25A1.875 1.875 0 0 1 18.75 5.625V7.5" />
      </svg>
    ),
  },
  {
    label: 'SwissAI',
    description: 'Production deployment',
    href: 'https://serving.swissai.cscs.ch/',
    external: true,
    icon: (
      <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" className="size-5">
        <path strokeLinecap="round" strokeLinejoin="round" d="M12 21a9.004 9.004 0 0 0 8.716-6.747M12 21a9.004 9.004 0 0 1-8.716-6.747M12 21c2.485 0 4.5-4.03 4.5-9S14.485 3 12 3m0 18c-2.485 0-4.5-4.03-4.5-9S9.515 3 12 3m0 0a8.997 8.997 0 0 1 7.843 4.582M12 3a8.997 8.997 0 0 0-7.843 4.582m15.686 0A11.953 11.953 0 0 1 12 10.5c-2.998 0-5.74-1.1-7.843-2.918m15.686 0A8.959 8.959 0 0 1 21 12c0 .778-.099 1.533-.284 2.253m0 0A17.919 17.919 0 0 1 12 16.5c-3.162 0-6.133-.815-8.716-2.247m0 0A9.015 9.015 0 0 1 3 12c0-1.605.42-3.113 1.157-4.418" />
      </svg>
    ),
  },
];

function SectionLabel({ children }: { children: ReactNode }) {
  return (
    <p className="text-sm font-medium uppercase tracking-widest text-fd-muted-foreground">
      {children}
    </p>
  );
}

export default function HomePage() {
  return (
    <main id="nd-main" className="flex min-h-dvh flex-col">
      {/* Hero */}
      <section className="relative overflow-hidden px-6 pt-24 pb-20">
        {/* Grid background */}
        <div className="grid-bg pointer-events-none absolute inset-0" />
        {/* Emerald glow */}
        <div className="pointer-events-none absolute -top-32 left-1/2 h-[500px] w-[500px] -translate-x-1/2 rounded-full bg-fd-primary/10 blur-[120px]" />

        <div className="relative mx-auto grid max-w-6xl items-center gap-12 lg:grid-cols-2">
          {/* Slogan */}
          <div className="text-center lg:text-left">
            <p className="mb-4 text-sm font-medium tracking-widest uppercase text-fd-primary">
              Peer-to-peer GPU mesh
            </p>
            <h1 className="font-display text-5xl leading-tight sm:text-6xl">
              Decentralized compute{' '}
              <span className="bg-gradient-to-r from-fd-primary to-fd-primary/60 bg-clip-text text-transparent">
                for AI
              </span>
            </h1>
            <p className="mx-auto mt-6 max-w-xl text-lg leading-relaxed text-fd-muted-foreground lg:mx-0">
              OpenTela pools GPUs across a peer-to-peer network into one
              OpenAI-compatible endpoint — with no central coordinator.
            </p>
            <div className="mt-10 flex flex-wrap items-center justify-center gap-4 lg:justify-start">
              <Link
                href="/docs"
                className="rounded-lg bg-fd-primary px-6 py-3 text-sm font-medium text-fd-primary-foreground shadow-lg shadow-fd-primary/20 transition-colors hover:bg-fd-primary/90"
              >
                Get Started
              </Link>
              <a
                href="https://github.com/eth-easl/OpenTela"
                target="_blank"
                rel="noopener noreferrer"
                className="inline-flex items-center gap-2 rounded-lg border border-fd-border bg-fd-background px-6 py-3 text-sm font-medium text-fd-foreground transition-colors hover:bg-fd-muted"
              >
                {GitHubIcon}
                GitHub
              </a>
            </div>
          </div>

          {/* Illustration */}
          <div className="flex justify-center lg:justify-end">
            {/* eslint-disable-next-line @next/next/no-img-element */}
            <img
              src="/hero.png"
              alt="OpenTela mesh — GPUs and compute nodes connected to a central AI accelerator"
              width={1005}
              height={781}
              className="h-auto w-full max-w-md lg:max-w-lg"
            />
          </div>
        </div>
      </section>

      {/* Live network stats — hidden when the analytics backend is down */}
      <LiveNetworkStats />

      {/* Features */}
      <section className="mx-auto w-full max-w-6xl px-6 py-16">
        <SectionLabel>Features</SectionLabel>
        <h2 className="mt-2 mb-12 max-w-xl text-2xl font-semibold tracking-tight">
          Everything you need for decentralized inference
        </h2>
        <div className="grid gap-6 sm:grid-cols-2 lg:grid-cols-3">
          {features.map((feature) => (
            <div
              key={feature.title}
              className="group rounded-xl border border-fd-border bg-fd-card p-6 transition-colors hover:border-fd-primary/40"
            >
              <div className="mb-4 flex size-10 items-center justify-center rounded-lg bg-fd-primary/10 text-fd-primary">
                {feature.icon}
              </div>
              <h3 className="mb-2 font-semibold">{feature.title}</h3>
              <p className="text-sm leading-relaxed text-fd-muted-foreground">
                {feature.description}
              </p>
            </div>
          ))}
        </div>
      </section>

      {/* News */}
      <section className="mx-auto w-full max-w-6xl px-6 py-16">
        <SectionLabel>News</SectionLabel>
        <h2 className="mt-2 mb-12 max-w-xl text-2xl font-semibold tracking-tight">
          From the OpenTela blog
        </h2>
        <div className="grid gap-6 sm:grid-cols-2">
          {news.map((item) => (
            <Link
              key={item.href}
              href={item.href}
              className="group flex flex-col rounded-xl border border-fd-border bg-fd-card p-6 transition-colors hover:border-fd-primary/40"
            >
              <span className="mb-3 inline-flex w-fit rounded-full bg-fd-primary/10 px-3 py-1 text-xs font-medium text-fd-primary">
                {item.tag}
              </span>
              <h3 className="mb-2 font-semibold leading-snug group-hover:text-fd-primary">
                {item.title}
              </h3>
              <p className="text-sm leading-relaxed text-fd-muted-foreground">
                {item.description}
              </p>
              <span className="mt-4 text-sm font-medium text-fd-primary">
                Read more →
              </span>
            </Link>
          ))}
        </div>
      </section>

      {/* Community */}
      <section className="mx-auto w-full max-w-6xl px-6 py-16">
        <SectionLabel>Community</SectionLabel>
        <h2 className="mt-2 mb-12 max-w-xl text-2xl font-semibold tracking-tight">
          Get involved
        </h2>
        <div className="grid gap-6 sm:grid-cols-3">
          {community.map((item) => (
            <a
              key={item.label}
              href={item.href}
              target={item.external ? '_blank' : undefined}
              rel={item.external ? 'noopener noreferrer' : undefined}
              className="group flex items-center gap-4 rounded-xl border border-fd-border bg-fd-card p-5 transition-colors hover:border-fd-primary/40"
            >
              <div className="flex size-10 shrink-0 items-center justify-center rounded-lg bg-fd-primary/10 text-fd-primary">
                {item.icon}
              </div>
              <div>
                <h3 className="font-semibold group-hover:text-fd-primary">
                  {item.label}
                </h3>
                <p className="text-sm text-fd-muted-foreground">
                  {item.description}
                </p>
              </div>
            </a>
          ))}
        </div>
      </section>

      {/* Footer */}
      <footer className="mt-auto border-t border-fd-border py-8">
        <div className="mx-auto flex max-w-6xl flex-col items-center justify-between gap-4 px-6 sm:flex-row">
          <p className="text-sm text-fd-muted-foreground">
            OpenTela &mdash; Open-source decentralized computing
          </p>
          <div className="flex gap-6">
            <Link
              href="/docs"
              className="-my-2 py-2 text-sm text-fd-muted-foreground transition-colors hover:text-fd-foreground"
            >
              Documentation
            </Link>
            <Link
              href="/observatory"
              className="-my-2 py-2 text-sm text-fd-muted-foreground transition-colors hover:text-fd-foreground"
            >
              Observatory
            </Link>
            <a
              href="https://cloud.opentela.ai/account"
              className="-my-2 py-2 text-sm text-fd-muted-foreground transition-colors hover:text-fd-foreground"
            >
              Account
            </a>
            <a
              href="https://github.com/eth-easl/OpenTela"
              target="_blank"
              rel="noopener noreferrer"
              className="-my-2 py-2 text-sm text-fd-muted-foreground transition-colors hover:text-fd-foreground"
            >
              GitHub
            </a>
          </div>
        </div>
      </footer>
    </main>
  );
}
