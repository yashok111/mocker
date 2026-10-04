export type ChangeJSON =
  | null
  | boolean
  | number
  | string
  | ChangeJSON[]
  | { [key: string]: ChangeJSON };
export type ChangeObject = { [key: string]: ChangeJSON };
export type ChangeSchema = {
  $ref?: string;
  type?: string | string[];
  const?: ChangeJSON;
  enum?: ChangeJSON[];
  properties?: Record<string, ChangeSchema>;
  required?: string[];
  additionalProperties?: boolean | ChangeSchema;
  propertyNames?: ChangeSchema;
  items?: ChangeSchema;
  oneOf?: ChangeSchema[];
  anyOf?: ChangeSchema[];
  allOf?: ChangeSchema[];
  not?: ChangeSchema;
  if?: ChangeSchema;
  then?: ChangeSchema;
  else?: ChangeSchema;
  minItems?: number;
  maxItems?: number;
  uniqueItems?: boolean;
  minLength?: number;
  maxLength?: number;
  minProperties?: number;
  maxProperties?: number;
  minimum?: number;
  maximum?: number;
  format?: string;
  pattern?: string;
  description?: string;
  discriminator?: { propertyName: string };
};
