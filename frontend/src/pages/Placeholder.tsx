import { motion } from "framer-motion";
import { Construction } from "lucide-react";

import { Card } from "@/components/ui/card";

export function PlaceholderPage({ title, blurb }: { title: string; blurb: string }) {
  return (
    <div className="space-y-8">
      <header>
        <motion.div
          initial={{ opacity: 0, y: 8 }}
          animate={{ opacity: 1, y: 0 }}
          transition={{ duration: 0.4 }}
        >
          <div className="mb-1 text-xs font-mono uppercase tracking-wider text-primary">
            coming soon
          </div>
          <h1 className="text-3xl font-semibold tracking-tight">{title}</h1>
          <p className="mt-2 text-sm text-muted-foreground">{blurb}</p>
        </motion.div>
      </header>

      <Card>
        <div className="flex flex-col items-center gap-3 p-16 text-center">
          <div className="flex size-14 items-center justify-center rounded-full bg-primary/10 text-primary ring-1 ring-primary/20">
            <Construction className="size-6" />
          </div>
          <div className="text-lg font-medium">In the roadmap</div>
          <div className="max-w-sm text-sm text-muted-foreground">
            This screen ships in a near-term milestone. See{" "}
            <a
              href="https://github.com/artyomsv/marauder/blob/main/docs/ROADMAP.md"
              className="font-medium text-foreground underline underline-offset-4"
            >
              docs/ROADMAP.md
            </a>{" "}
            for the plan.
          </div>
        </div>
      </Card>
    </div>
  );
}
