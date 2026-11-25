import React from 'react';

interface OrchestratorContextProps {
  metadata?: {
    [k: string]: unknown;
  };
  className?: string;
}

export const OrchestratorContext: React.FC<OrchestratorContextProps> = ({ 
  metadata, 
  className = "" 
}) => {
  if (!metadata) {
    return null;
  }

  const orchestratorPhase = metadata.orchestrator_phase as string;
  const step = metadata.orchestrator_step as number;
  const agentName = metadata.orchestrator_agent_name as string;

  if (!orchestratorPhase) {
    return null;
  }

  const getPhaseInfo = (phase: string) => {
    const phases = {
      // Orchestrator phases
      'planning': { icon: '', name: 'Planning', color: 'blue' },
      'execution': { icon: '', name: 'Execution', color: 'green' },
      'validation': { icon: '', name: 'Validation', color: 'purple' },
      'organizer': { icon: '', name: 'Activity', color: 'orange' },
      // Workflow phases
      'todo_planner': { icon: '', name: 'Todo Planning', color: 'indigo' },
      'todo_execution': { icon: '', name: 'Todo Execution', color: 'emerald' },
      'todo_validation': { icon: '', name: 'Todo Validation', color: 'violet' },
      'workspace_update': { icon: '', name: 'Workspace Update', color: 'amber' },
      // Todo Planner Orchestrator phases (Planning & Todo Creation)
      'todo_planner_planning': { icon: '', name: 'Planning', color: 'blue' },
      'todo_planner_execution': { icon: '', name: 'Execution', color: 'green' },
      'todo_planner_validation': { icon: '', name: 'Basic Validation', color: 'purple' },
      'todo_planner_critique': { icon: '', name: 'Enhanced Critique', color: 'violet' },
      'todo_planner_writer': { icon: '', name: 'Writing', color: 'orange' },
      'todo_planner_cleanup': { icon: '', name: 'Cleanup', color: 'gray' }
    };
    return phases[phase as keyof typeof phases] || { icon: '', name: phase, color: 'gray' };
  };

  const getStepDisplay = (phase: string, step: number) => {
    // Todo Planner Orchestrator has 6 steps (including critique)
    if (phase.startsWith('todo_planner_')) {
      const stepMap = {
        'todo_planner_planning': 1,
        'todo_planner_execution': 2,
        'todo_planner_validation': 3,
        'todo_planner_critique': 4,
        'todo_planner_writer': 5,
        'todo_planner_cleanup': 6
      };
      const currentStep = stepMap[phase as keyof typeof stepMap] || step + 1;
      return `Step ${currentStep}/6`;
    }
    
    // Default step display
    return step !== undefined ? `Step ${step + 1}` : '';
  };

  const phaseInfo = getPhaseInfo(orchestratorPhase);

  const getPhaseColorClasses = (color: string) => {
    const colorMap = {
      'blue': 'bg-blue-100 dark:bg-blue-900 text-blue-800 dark:text-blue-200',
      'green': 'bg-green-100 dark:bg-green-900 text-green-800 dark:text-green-200',
      'purple': 'bg-purple-100 dark:bg-purple-900 text-purple-800 dark:text-purple-200',
      'orange': 'bg-orange-100 dark:bg-orange-900 text-orange-800 dark:text-orange-200',
      'indigo': 'bg-indigo-100 dark:bg-indigo-900 text-indigo-800 dark:text-indigo-200',
      'emerald': 'bg-emerald-100 dark:bg-emerald-900 text-emerald-800 dark:text-emerald-200',
      'violet': 'bg-violet-100 dark:bg-violet-900 text-violet-800 dark:text-violet-200',
      'amber': 'bg-amber-100 dark:bg-amber-900 text-amber-800 dark:text-amber-200',
      'gray': 'bg-muted text-foreground'
    };
    return colorMap[color as keyof typeof colorMap] || colorMap.gray;
  };

  return (
    <div className={`text-xs text-muted-foreground mb-2 flex items-center ${className}`}>
      <span className={`${getPhaseColorClasses(phaseInfo.color)} px-2 py-1 rounded mr-2 font-medium`}>
        {phaseInfo.name}
      </span>
      {agentName && (
        <span className="text-muted-foreground mr-2">
          {agentName}
        </span>
      )}
      {step !== undefined && (
        <span className="text-muted-foreground mr-2">
          {getStepDisplay(orchestratorPhase, step)}
        </span>
      )}
    </div>
  );
};
