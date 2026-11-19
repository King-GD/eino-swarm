export type InsertMode = "steer" | "interrupt"
export type DeliveryMode = "queue" | InsertMode

export const insertModes = ["steer", "interrupt"] as const

export type DeliveryOptions = { deliveryMode?: DeliveryMode; expectedTurnId?: string }
