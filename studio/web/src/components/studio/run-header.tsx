// The run page header: identity, model, status, usage, provenance —
// and the local-recording note (D8): this content is on disk because
// store.Record was installed, and the page says so.
import { Link } from "@tanstack/react-router"
import { ArrowUpRight } from "lucide-react"

import type { RunDoc } from "@/lib/api"
import { duration, relativeTime, usageSummary } from "@/lib/format"
import { StatusDot } from "@/components/studio/runs-table"
import { Badge } from "@/components/ui/badge"
import {
  Breadcrumb,
  BreadcrumbItem,
  BreadcrumbLink,
  BreadcrumbList,
  BreadcrumbSeparator,
} from "@/components/ui/breadcrumb"

function shortHash(hash: string | undefined): string {
  return hash ? hash.slice(0, 8) : "—"
}

export function RunHeader({ doc }: { doc: RunDoc }) {
  return (
    <div className="space-y-2">
      {doc.parent_id && (
        <Breadcrumb>
          <BreadcrumbList>
            <BreadcrumbItem>
              <BreadcrumbLink
                render={<Link to="/runs/$id" params={{ id: doc.parent_id }} />}
              >
                {doc.parent_id}
              </BreadcrumbLink>
            </BreadcrumbItem>
            <BreadcrumbSeparator />
            <BreadcrumbItem>
              <span className="font-mono text-xs">{doc.id}</span>
            </BreadcrumbItem>
          </BreadcrumbList>
        </Breadcrumb>
      )}
      <div className="flex flex-wrap items-baseline gap-x-3 gap-y-1">
        <h1 className="font-mono text-base font-medium tracking-tight">
          {doc.id}
        </h1>
        <span className="flex items-center gap-1.5 text-sm">
          <StatusDot status={doc.status} />
          {doc.status}
        </span>
        <span>{doc.agent || "unnamed agent"}</span>
        <Badge variant="outline" className="font-mono text-[11px] font-normal">
          {doc.model.provider}/{doc.model.name}
        </Badge>
        <span className="font-mono text-xs text-muted-foreground">
          {doc.steps} steps · {usageSummary(doc.usage)} ·{" "}
          {relativeTime(doc.started)}
          {doc.finished ? ` · ${duration(doc.started, doc.finished)}` : ""}
        </span>
      </div>
      <div className="flex flex-wrap items-center gap-x-4 gap-y-1 text-xs text-faint">
        <span className="font-mono">
          manifest {shortHash(doc.manifest_hash)}
        </span>
        {doc.weft_version ? (
          <span className="font-mono">weft {doc.weft_version}</span>
        ) : null}
        {Object.entries(doc.tags).map(([k, v]) => (
          <span key={k} className="font-mono">
            {k}={v}
          </span>
        ))}
        <span>recorded locally by store.Record — content included</span>
      </div>
      {doc.err ? (
        <div className="rounded-md border border-status-bad/30 bg-status-bad/5 px-3 py-2 font-mono text-xs text-status-bad">
          {doc.err}
        </div>
      ) : null}
      {doc.children.length > 0 && (
        <div className="flex flex-wrap items-center gap-2 text-xs">
          <span className="eyebrow">children</span>
          {doc.children.map((c) => (
            <Link
              key={c.id}
              to="/runs/$id"
              params={{ id: c.id }}
              className="flex items-center gap-0.5 font-mono text-[11px] text-thread-ink hover:underline"
            >
              {c.agent} · {c.id}
              <ArrowUpRight className="size-3" data-slot="icon" />
            </Link>
          ))}
        </div>
      )}
    </div>
  )
}
