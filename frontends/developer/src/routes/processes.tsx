import { createFileRoute } from "@tanstack/react-router";
import { useState } from "react";
import { Cpu, ChevronDown, ChevronRight, RefreshCw } from "lucide-react";
import { useCognitiveProcesses, useCognitiveProcessDetails } from "@/hooks/useCognitiveApi";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";

export const Route = createFileRoute("/processes")({
  component: ProcessesPage,
});

function ProcessesPage() {
  const { data: processes, isLoading, error, refetch, isFetching } = useCognitiveProcesses();
  const [expandedProcessId, setExpandedProcessId] = useState<string | null>(null);

  const toggleExpand = (processId: string) => {
    setExpandedProcessId(expandedProcessId === processId ? null : processId);
  };

  return (
    <div className="flex h-full flex-col">
      <div className="px-5 pb-5 pt-8 md:px-10 md:pt-10">
        <div className="flex items-start justify-between gap-6">
          <div>
            <h1 className="console-title text-4xl leading-tight text-foreground md:text-5xl">
              Cognitive Processes
            </h1>
            <p className="console-subtitle mt-3 text-base md:text-lg">
              Monitor cognitive memory processing pipelines
            </p>
          </div>
          <Button
            variant="ghost"
            size="sm"
            onClick={() => refetch()}
            disabled={isFetching}
            className="mt-3"
          >
            <RefreshCw className={`h-4 w-4 ${isFetching ? "animate-spin" : ""}`} />
            Refresh
          </Button>
        </div>
      </div>

      <div className="flex-1 overflow-y-auto px-5 pb-8 md:px-10">
        {isLoading && (
          <div className="flex items-center justify-center py-12">
            <div className="text-center">
              <div className="mb-4 inline-block h-8 w-8 animate-spin rounded-full border-4 border-primary border-t-transparent"></div>
              <p className="text-sm text-muted-foreground">Loading processes...</p>
            </div>
          </div>
        )}

        {error && (
          <div className="console-panel rounded-xl p-4 text-center">
            <p className="text-sm text-destructive">
              Failed to load processes: {error instanceof Error ? error.message : "Unknown error"}
            </p>
          </div>
        )}

        {!isLoading && !error && processes && processes.length === 0 && (
          <div className="console-panel rounded-xl p-12 text-center">
            <p className="text-muted-foreground">No processes found</p>
          </div>
        )}

        {!isLoading && !error && processes && processes.length > 0 && (
          <div className="space-y-3">
            {processes.map((process) => (
              <ProcessCard
                key={process.id}
                process={process}
                isExpanded={expandedProcessId === process.id}
                onToggle={() => toggleExpand(process.id)}
              />
            ))}
          </div>
        )}
      </div>
    </div>
  );
}

interface ProcessCardProps {
  process: {
    id: string;
    displayName: string;
    description: string;
    state: "ENABLED" | "DISABLED";
  };
  isExpanded: boolean;
  onToggle: () => void;
}

function ProcessCard({ process, isExpanded, onToggle }: ProcessCardProps) {
  const { data: details, isLoading: isLoadingDetails } = useCognitiveProcessDetails(
    isExpanded ? process.id : undefined
  );

  return (
    <div className="console-panel rounded-xl overflow-hidden">
      <button
        onClick={onToggle}
        className="w-full p-5 text-left transition-colors hover:bg-sage-soft/20"
      >
        <div className="flex items-start justify-between gap-4">
          <div className="min-w-0 flex-1">
            <div className="mb-2 flex flex-wrap items-center gap-2">
              {isExpanded ? (
                <ChevronDown className="h-5 w-5 text-muted-foreground shrink-0" />
              ) : (
                <ChevronRight className="h-5 w-5 text-muted-foreground shrink-0" />
              )}
              <Cpu className="h-5 w-5 text-primary" strokeWidth={1.55} />
              <Badge variant={process.state === "ENABLED" ? "default" : "secondary"}>
                {process.state}
              </Badge>
            </div>
            <h3 className="text-lg font-semibold text-foreground">{process.displayName}</h3>
            <p className="mt-1 text-sm text-muted-foreground">{process.description}</p>
          </div>
        </div>
      </button>

      {isExpanded && (
        <div className="border-t border-[rgba(43,39,34,0.1)] px-5 pb-5">
          {isLoadingDetails && (
            <div className="py-8 text-center">
              <div className="mb-2 inline-block h-6 w-6 animate-spin rounded-full border-2 border-primary border-t-transparent"></div>
              <p className="text-sm text-muted-foreground">Loading details...</p>
            </div>
          )}
          {!isLoadingDetails && details && (
            <ProcessDetails details={details} />
          )}
        </div>
      )}
    </div>
  );
}

interface ProcessDetailsProps {
  details: {
    id: string;
    displayName: string;
    description: string;
    state: string;
    details: {
      mode?: string;
      lastRunTime?: string;
      lastRunStatus?: string;
      lastRunUserId?: string;
      eventStreamConnected?: boolean;
      eventsAccepted?: number;
      activeWindows?: number;
      totalQueues?: number;
      activeQueues?: number;
      pendingJobs?: number;
      resourceTypes?: Record<string, {
        type: string;
        provider?: string;
        modelName?: string;
        model?: string;
        endpoint?: string;
        prompt?: string;
      }>;
    };
  };
}

function ProcessDetails({ details }: ProcessDetailsProps) {
  const [expandedPrompts, setExpandedPrompts] = useState<Set<string>>(new Set());

  const togglePrompt = (key: string) => {
    setExpandedPrompts((prev) => {
      const next = new Set(prev);
      if (next.has(key)) {
        next.delete(key);
      } else {
        next.add(key);
      }
      return next;
    });
  };

  return (
    <div className="mt-4 space-y-4">
      {/* Status Information */}
      <div className="space-y-2">
        <h4 className="text-sm font-semibold text-foreground">Status</h4>
        <div className="grid grid-cols-2 gap-3 text-sm">
          {details.details.mode && (
            <div>
              <span className="text-muted-foreground">Mode:</span>{" "}
              <span className="font-medium text-foreground">{details.details.mode}</span>
            </div>
          )}
          {details.details.lastRunTime && (
            <div>
              <span className="text-muted-foreground">Last Run:</span>{" "}
              <span className="font-medium text-foreground">{details.details.lastRunTime}</span>
            </div>
          )}
          {details.details.lastRunStatus && (
            <div>
              <span className="text-muted-foreground">Last Status:</span>{" "}
              <span className="font-medium text-foreground">{details.details.lastRunStatus}</span>
            </div>
          )}
          {details.details.lastRunUserId && (
            <div>
              <span className="text-muted-foreground">Last User:</span>{" "}
              <span className="font-medium text-foreground">{details.details.lastRunUserId}</span>
            </div>
          )}
          {details.details.eventStreamConnected !== undefined && (
            <div>
              <span className="text-muted-foreground">Event Stream:</span>{" "}
              <Badge variant={details.details.eventStreamConnected ? "default" : "secondary"}>
                {details.details.eventStreamConnected ? "Connected" : "Disconnected"}
              </Badge>
            </div>
          )}
          {details.details.eventsAccepted !== undefined && (
            <div>
              <span className="text-muted-foreground">Events Accepted:</span>{" "}
              <span className="font-medium text-foreground">{details.details.eventsAccepted}</span>
            </div>
          )}
          {details.details.activeWindows !== undefined && (
            <div>
              <span className="text-muted-foreground">Active Windows:</span>{" "}
              <span className="font-medium text-foreground">{details.details.activeWindows}</span>
            </div>
          )}
          {details.details.totalQueues !== undefined && (
            <div>
              <span className="text-muted-foreground">Total Queues:</span>{" "}
              <span className="font-medium text-foreground">{details.details.totalQueues}</span>
            </div>
          )}
          {details.details.activeQueues !== undefined && (
            <div>
              <span className="text-muted-foreground">Active Queues:</span>{" "}
              <span className="font-medium text-foreground">{details.details.activeQueues}</span>
            </div>
          )}
          {details.details.pendingJobs !== undefined && (
            <div>
              <span className="text-muted-foreground">Pending Jobs:</span>{" "}
              <span className="font-medium text-foreground">{details.details.pendingJobs}</span>
            </div>
          )}
        </div>
      </div>

      {/* Resource Types */}
      {details.details.resourceTypes && Object.keys(details.details.resourceTypes).length > 0 && (
        <div className="space-y-3">
          <h4 className="text-sm font-semibold text-foreground">Resources</h4>
          {Object.entries(details.details.resourceTypes).map(([key, resource]) => (
            <div key={key} className="rounded-lg border border-[rgba(43,39,34,0.12)] bg-white/50 p-4">
              <div className="mb-2 flex items-center gap-2">
                <h5 className="font-medium text-foreground capitalize">{key}</h5>
                <Badge variant="outline">{resource.type}</Badge>
              </div>
              <div className="space-y-1 text-sm">
                {resource.provider && (
                  <div>
                    <span className="text-muted-foreground">Provider:</span>{" "}
                    <span className="text-foreground">{resource.provider}</span>
                  </div>
                )}
                {resource.model && (
                  <div>
                    <span className="text-muted-foreground">Model:</span>{" "}
                    <span className="text-foreground">{resource.model}</span>
                  </div>
                )}
                {resource.modelName && (
                  <div>
                    <span className="text-muted-foreground">Model Name:</span>{" "}
                    <span className="text-foreground">{resource.modelName}</span>
                  </div>
                )}
                {resource.endpoint && (
                  <div>
                    <span className="text-muted-foreground">Endpoint:</span>{" "}
                    <span className="font-mono text-xs text-foreground">{resource.endpoint}</span>
                  </div>
                )}
                {resource.prompt && (
                  <div className="mt-3">
                    <button
                      onClick={() => togglePrompt(key)}
                      className="flex items-center gap-2 text-sm font-medium text-primary hover:underline"
                    >
                      {expandedPrompts.has(key) ? (
                        <>
                          <ChevronDown className="h-4 w-4" />
                          Hide Prompt
                        </>
                      ) : (
                        <>
                          <ChevronRight className="h-4 w-4" />
                          Show Prompt
                        </>
                      )}
                    </button>
                    {expandedPrompts.has(key) && (
                      <pre className="mt-2 max-h-96 overflow-auto rounded-md bg-sage-soft/30 p-3 text-xs text-foreground">
                        {resource.prompt}
                      </pre>
                    )}
                  </div>
                )}
              </div>
            </div>
          ))}
        </div>
      )}
    </div>
  );
}

// Made with Bob
