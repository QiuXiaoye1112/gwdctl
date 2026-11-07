import {
  Alert,
  AlertDescription,
  AlertIcon,
  Box,
  CloseButton,
  notificationService,
} from "@hope-ui/solid"
import { JSXElement } from "solid-js"
import { alphaBgColor, firstUpperCase } from "."

const showCompact = (
  status: "success" | "danger" | "info" | "warning",
  message: string,
) => {
  notificationService.show({
    render: (props) => (
      <Alert
        status={status}
        shadow="$md"
        rounded="$lg"
        py="$1_5"
        px="$3"
        mb="56px"
        fontSize="$sm"
        w="auto"
        maxW="85vw"
      >
        <AlertIcon boxSize="$4" mr="$2" />
        <AlertDescription mr="$1">{firstUpperCase(message)}</AlertDescription>
        <CloseButton size="sm" onClick={props.close} />
      </Alert>
    ),
  })
}

const notify = {
  render: (element: JSXElement) => {
    notificationService.show({
      render: (props) => {
        return (
          <Box
            css={{
              display: "flex",
              backdropFilter: "blur(8px)",
              backgroundColor: alphaBgColor(),
              boxShadow: "$md",
              borderRadius: "$lg",
              padding: "$2 $3",
            }}
          >
            <div
              style={{
                "flex-grow": 1,
                display: "flex",
                "align-items": "center",
              }}
            >
              <div style={{ margin: "auto" }}>{element}</div>
            </div>
            <div style={{ display: "inline-block", padding: "2px" }}>
              <CloseButton
                size="sm"
                style={{ float: "right" }}
                onClick={props.close}
              />
            </div>
          </Box>
        )
      },
    })
  },
  success: (message: string) => {
    showCompact("success", message)
  },
  error: (message: string) => {
    showCompact("danger", message)
  },
  info: (message: string) => {
    showCompact("info", message)
  },
  warning: (message: string) => {
    showCompact("warning", message)
  },
}

export { notify }
