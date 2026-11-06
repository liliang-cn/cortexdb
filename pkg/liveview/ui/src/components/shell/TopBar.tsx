import { useState } from "react";
import { Menu } from "lucide-react";
import { Link, NavLink } from "react-router";
import { Wordmark } from "@/components/Logo";
import { Button } from "@/components/ui/button";
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from "@/components/ui/sheet";
import { useCounts, useLinkView } from "@/live";
import { cn } from "@/lib/utils";
import { ConnectionPill } from "./ConnectionPill";
import { APP_NAV, PAGE_LINKS } from "./nav";
import { ThemeToggle } from "./ThemeToggle";

const linkBase =
  "relative inline-flex h-12 items-center gap-1.5 px-3 text-sm font-medium text-muted-foreground transition-colors hover:text-foreground " +
  "outline-none focus-visible:text-foreground focus-visible:ring-[3px] focus-visible:ring-ring/50 focus-visible:ring-inset";

function Counts({ className }: { className?: string }) {
  const { nodes, edges } = useCounts();
  return (
    <div className={cn("font-mono text-xs tabular-nums text-muted-foreground", className)} aria-label="Graph size">
      <span className="text-foreground">{nodes.toLocaleString()}</span> nodes
      <span className="mx-1.5 text-border">/</span>
      <span className="text-foreground">{edges.toLocaleString()}</span> edges
    </div>
  );
}

export function TopBar() {
  const [open, setOpen] = useState(false);
  const link = useLinkView();

  return (
    <header className="sticky top-0 z-30 flex h-12 shrink-0 items-center gap-2 border-b bg-card/90 px-3 backdrop-blur sm:px-4">
      <Link to="/" className="mr-2 rounded-md outline-none focus-visible:ring-[3px] focus-visible:ring-ring/50" aria-label="CortexDB, graph">
        <Wordmark />
      </Link>

      <nav aria-label="Pages" className="hidden items-center lg:flex">
        {APP_NAV.map(({ to, label, end }) => (
          <NavLink
            key={to}
            to={to}
            end={end}
            className={({ isActive }) =>
              cn(linkBase, isActive && "text-foreground after:absolute after:inset-x-3 after:bottom-0 after:h-0.5 after:rounded-full after:bg-primary")
            }
          >
            {label}
          </NavLink>
        ))}
        <span aria-hidden className="mx-2 h-4 w-px bg-border" />
        {PAGE_LINKS.map(({ href, label }) => (
          <a key={href} href={href} className={cn(linkBase, "font-normal")}>
            {label}
          </a>
        ))}
      </nav>

      <div className="ml-auto flex items-center gap-2">
        <Counts className="hidden md:block" />
        <ConnectionPill />
        <ThemeToggle />
        <Button variant="ghost" size="icon" className="lg:hidden" aria-label="Menu" onClick={() => setOpen(true)}>
          <Menu />
        </Button>
      </div>

      <Sheet open={open} onOpenChange={setOpen}>
        <SheetContent side="right" className="w-72 gap-0">
          <SheetHeader className="border-b">
            <SheetTitle><Wordmark /></SheetTitle>
            <SheetDescription className="sr-only">Navigation</SheetDescription>
          </SheetHeader>
          <nav aria-label="Pages" className="flex flex-col p-2">
            {APP_NAV.map(({ to, label, icon: Icon, end }) => (
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
                {label}
              </NavLink>
            ))}
            <div className="my-2 h-px bg-border" />
            {PAGE_LINKS.map(({ href, label }) => (
              <a key={href} href={href} className="rounded-md px-3 py-2.5 text-sm text-muted-foreground outline-none hover:bg-muted focus-visible:ring-[3px] focus-visible:ring-ring/50">
                {label}
              </a>
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
