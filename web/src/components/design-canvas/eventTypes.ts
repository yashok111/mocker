import type {
  DesignScenarioEventBinding,
  DesignScenarioEventChannel,
  DesignScenarioEventContract,
  DesignScenarioEventMessage,
  DesignScenarioEventModel,
  DesignScenarioEventOperation,
  DesignScenarioEventSchema,
  DesignScenarioEventServer,
} from "@/api/generated/schemas";

export type EventModel = DesignScenarioEventModel;
export type EventServer = DesignScenarioEventServer;
export type EventChannel = DesignScenarioEventChannel;
export type EventMessage = DesignScenarioEventMessage;
export type EventSchema = DesignScenarioEventSchema;
export type EventContract = DesignScenarioEventContract;
export type EventOperation = DesignScenarioEventOperation;
export type EventBinding = DesignScenarioEventBinding;
export type EventCollection = keyof EventModel;
