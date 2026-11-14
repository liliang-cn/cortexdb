import { Fragment, useState } from "react";
import { Menu } from "lucide-react";
import { Link, NavLink } from "react-router";
import { Wordmark } from "@/components/Logo";
import { Button } from "@/components/ui/button";
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from "@/components/ui/sheet";
import { useCounts, useLinkView } from "@/live";
import { useLang, useText } from "@/lib/i18n";
import { SHELL_TEXT } from "@/lib/shellStrings";
import { formatNumber } from "@/lib/time";
import { cn } from "@/lib/utils";
import { LangToggle } from "./LangToggle";
import { PaletteButton } from "./CommandPalette";
import { ConnectionPill } from "./ConnectionPill";
import { visibleNav } from "./nav";
import { ThemeToggle } from "./ThemeToggle";

const linkBase =
  "relative inline-flex h-12 items-center gap-1.5 px-3 text-sm font-medium text-muted-foreground transition-colors hover:text-foreground " +
  "outline-none focus-visible:text-foreground focus-visible:ring-[3px] focus-visible:ring-ring/50 focus-visible:ring-inset";

function Counts({ className }: { className?: string }) {
  const { nodes, edges } = useCounts();
  const lang = useLang();
  const t = SHELL_TEXT[lang];
  return (
    <div className={cn("font-mono text-xs tabular-nums text-muted-foreground", className)} aria-label={t.graphSize}>
      <span className="text-foreground">{formatNumber(lang, nodes)}</span> {t.nodes}
      <span className="mx-1.5 text-border">/</span>
      <span className="text-foreground">{formatNumber(lang, edges)}</span> {t.edges}
    </div>
  );
}

export function TopBar() {
  const [open, setOpen] = useState(false);
  const link = useLinkView();
  const NAV = visibleNav();
  const t = useText(SHELL_TEXT);

  return (
    <header className="sticky top-0 z-30 flex h-12 shrink-0 items-center gap-2 border-b bg-card/90 px-3 backdrop-blur sm:px-4">
      <Link to="/" className="mr-2 rounded-md outline-none focus-visible:ring-[3px] focus-visible:ring-ring/50" aria-label={t.home}>
        <Wordmark />
      </Link>

      {/* Eight pages, the counts and the controls fit one line from 1280px; below that the pages are in the menu. */}
      <nav aria-label={t.pages} className="hidden items-center xl:flex">
        {NAV.map((group, i) => (
          <Fragment key={i}>
            {i > 0 && <span aria-hidden className="mx-2 h-4 w-px bg-border" />}
            {group.map(({ to, end }) => (
              <NavLink
                key={to}
                to={to}
                end={end}
                className={({ isActive }) =>
                  cn(linkBase, isActive && "text-foreground after:absolute after:inset-x-3 after:bottom-0 after:h-0.5 after:rounded-full after:bg-primary")
                }
              >
                {t.nav[to]}
              </NavLink>
            ))}
          </Fragment>
        ))}
      </nav>

      <div className="ml-auto flex items-center gap-2">
        <Counts className="hidden md:block" />
        <PaletteButton />
        <ConnectionPill />
        <LangToggle />
        <ThemeToggle />
        <Button variant="ghost" size="icon" className="xl:hidden" aria-label={t.menu} onClick={() => setOpen(true)}>
          <Menu />
        </Button>
      </div>

      <Sheet open={open} onOpenChange={setOpen}>
        <SheetContent side="right" className="w-72 gap-0">
          <SheetHeader className="border-b">
            <SheetTitle><Wordmark /></SheetTitle>
            <SheetDescription className="sr-only">{t.navigation}</SheetDescription>
          </SheetHeader>
          <nav aria-label={t.pages} className="flex flex-col p-2">
            {NAV.map((group, i) => (
              <Fragment key={i}>
                {i > 0 && <div className="my-2 h-px bg-border" />}
                {group.map(({ to, icon: Icon, end }) => (
                  <NavLink
                    key={to}
                    to={to}
                    end={end}
                    onClick={() => setOpen(false)}
                    className={({ isActive }) =>
                      cn(
                        "flex items-center gap-3 rounded-md px-3 py-2.5 text-sm font-medium text-muted-foreground",
                        "outline-none focus-visible:ring-[3px] focus-visible:ring-ring/50",
                        isActive && "bg-accent text-accent-foreground",
                      )
                    }
                  >
                    <Icon className="size-4" />
                    {t.nav[to]}
                  </NavLink>
                ))}
              </Fragment>
            ))}
          </nav>
          <div className="mt-auto space-y-2 border-t p-4 text-xs text-muted-foreground">
            <Counts />
            <p>{link.detail}</p>
          </div>
        </SheetContent>
      </Sheet>
    </header>
  );
}
