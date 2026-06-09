import * as React from "react";
import { Input } from "@/components/ui/input";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from "@/components/ui/popover";
import { Checkbox } from "@/components/ui/checkbox";
import { Label } from "@/components/ui/label";
import { Button } from "@/components/ui/button";
import { ChevronDown, X } from "lucide-react";

/**
 * FilterBar is the only React island on the index page. It owns the
 * search/tier/engine-filter state and updates the visibility of the
 * server-rendered work cards via data-attributes. Keeps the card list
 * out of the JS bundle (server-rendered as Astro), uses React only for
 * the interactive controls that benefit from shadcn primitives.
 */

interface FilterBarProps {
  engines: string[];
}

export function FilterBar({ engines }: FilterBarProps) {
  const [search, setSearch] = React.useState("");
  const [tier, setTier] = React.useState<string>("all");
  const [selectedEngines, setSelectedEngines] = React.useState<Set<string>>(
    new Set(),
  );
  const [inclAntagonist, setInclAntagonist] = React.useState(false);
  const [shownCount, setShownCount] = React.useState<number | null>(null);

  React.useEffect(() => {
    const cards = document.querySelectorAll<HTMLElement>("[data-card]");
    const emptyState = document.getElementById("catalog-empty-state");
    const lowerSearch = search.trim().toLowerCase();
    const hasAnyFilter =
      lowerSearch !== "" || tier !== "all" || selectedEngines.size > 0;

    // Curated landing: when no filter is active, hide all cards and show the
    // empty-state prompt instead of dumping ~356 cards alphabetically. The
    // paragons section above stays as the curated entry point.
    if (!hasAnyFilter) {
      cards.forEach((card) => {
        card.style.display = "none";
      });
      if (emptyState) emptyState.style.display = "";
      setShownCount(null);
      return;
    }
    if (emptyState) emptyState.style.display = "none";

    let shown = 0;
    cards.forEach((card) => {
      const text = card.dataset.text ?? "";
      const cardEngines = (card.dataset.engines ?? "").split("|").filter(Boolean);
      const cardTiers = (card.dataset.tiers ?? "").split("|").filter(Boolean);

      const matchesSearch = !lowerSearch || text.includes(lowerSearch);
      const matchesTier = tier === "all" || cardTiers.includes(tier);

      let matchesEngine = true;
      if (selectedEngines.size > 0) {
        const cardEngineSet = new Set(
          inclAntagonist
            ? cardEngines.map((e) => e.replace(/-antagonist-mode$/, ""))
            : cardEngines,
        );
        matchesEngine = Array.from(selectedEngines).some((e) =>
          cardEngineSet.has(e),
        );
      }

      const visible = matchesSearch && matchesTier && matchesEngine;
      card.style.display = visible ? "" : "none";
      if (visible) shown++;
    });
    setShownCount(shown);
  }, [search, tier, selectedEngines, inclAntagonist]);

  const toggleEngine = (engine: string) => {
    setSelectedEngines((prev) => {
      const next = new Set(prev);
      if (next.has(engine)) next.delete(engine);
      else next.add(engine);
      return next;
    });
  };

  const clearEngines = () => {
    setSelectedEngines(new Set());
    setInclAntagonist(false);
  };

  const engineLabel =
    selectedEngines.size === 0
      ? "filter by engine"
      : selectedEngines.size === 1
        ? Array.from(selectedEngines)[0]
        : `${selectedEngines.size} engines`;

  return (
    <div className="font-sans max-w-[760px] mx-auto px-[22px] py-[14px] sticky top-0 z-10 bg-background border-b border-border">
      <div className="flex flex-wrap items-center gap-[10px]">
        <Input
          type="search"
          placeholder="search work or author…"
          aria-label="search by work title or author"
          value={search}
          onChange={(e) => setSearch(e.target.value)}
          className="flex-1 min-w-[180px] h-9 bg-card border-input"
        />
        <Select value={tier} onValueChange={setTier}>
          <SelectTrigger
            className="h-9 min-w-[140px] bg-card border-input"
            aria-label="filter by evidence tier"
          >
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="all">all evidence</SelectItem>
            <SelectItem value="✓">&#10003; slot-proven only</SelectItem>
            <SelectItem value="~">&tilde; reviewed only</SelectItem>
          </SelectContent>
        </Select>
        <Popover>
          <PopoverTrigger asChild>
            <Button
              variant="outline"
              size="default"
              className="h-9 min-w-[160px] justify-between bg-card border-input text-foreground hover:bg-card font-normal"
              aria-label="filter cards by engine"
            >
              <span>{engineLabel}</span>
              <ChevronDown className="size-3 text-dim" />
            </Button>
          </PopoverTrigger>
          <PopoverContent
            className="w-[300px] p-3"
            align="start"
            sideOffset={6}
          >
            <div className="grid grid-cols-2 gap-x-3 gap-y-1">
              {engines.map((engine) => (
                <label
                  key={engine}
                  className="flex items-center gap-2 text-[13px] text-ink-soft py-1 cursor-pointer"
                >
                  <Checkbox
                    checked={selectedEngines.has(engine)}
                    onCheckedChange={() => toggleEngine(engine)}
                  />
                  <span>{engine}</span>
                </label>
              ))}
            </div>
            <div className="mt-2 pt-2 border-t border-border flex items-center gap-2">
              <Checkbox
                id="incl-antagonist"
                checked={inclAntagonist}
                onCheckedChange={(c) => setInclAntagonist(c === true)}
              />
              <Label
                htmlFor="incl-antagonist"
                className="text-[12px] text-dim cursor-pointer"
              >
                include antagonist-mode variants
              </Label>
            </div>
            <Button
              variant="ghost"
              size="sm"
              className="w-full mt-2 text-dim hover:text-foreground"
              onClick={clearEngines}
            >
              <X className="size-3" /> clear
            </Button>
          </PopoverContent>
        </Popover>
        {shownCount !== null && (
          <span className="text-dim text-[12.5px] whitespace-nowrap">
            {shownCount} shown
          </span>
        )}
      </div>
    </div>
  );
}
