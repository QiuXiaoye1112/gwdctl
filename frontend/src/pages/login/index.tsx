import {
  Image,
  Center,
  Flex,
  Heading,
  Input,
  Button,
  useColorModeValue,
  HStack,
  VStack,
} from "@hope-ui/solid"
import { createEffect, createMemo, createSignal, onMount, Show } from "solid-js"
import { SwitchColorMode, SwitchLanguageWhite } from "~/components"
import { useLoading, useT, useTitle, useRouter } from "~/hooks"
import {
  changeToken,
  r,
  notify,
  handleRespWithoutAuthAndNotify,
  base_path,
  hashPwd,
} from "~/utils"
import { Resp } from "~/types"
import LoginBg from "./LoginBg"
import { getSetting } from "~/store"

const Login = () => {
  const logos = getSetting("logo").split("\n")
  const logo = useColorModeValue(logos[0], logos[logos.length - 1])
  const t = useT()
  const title = createMemo(() => {
    return `${t("login.login_to")} ${getSetting("site_title")}`
  })
  useTitle(title)
  const bgColor = useColorModeValue("white", "$neutral1")
  const [username, setUsername] = createSignal("")
  const [password, setPassword] = createSignal("")
  const [opt, setOpt] = createSignal("")
  const [needOpt, setNeedOpt] = createSignal(false)
  let totpInputRef: HTMLInputElement | undefined

  // Purge any legacy plaintext credentials previously saved in localStorage;
  // credential storage is delegated to the browser's native password manager.
  onMount(() => {
    try {
      localStorage.removeItem("password")
      localStorage.removeItem("username")
      localStorage.removeItem("remember-pwd")
    } catch {}
  })

  createEffect(() => {
    if (needOpt()) {
      queueMicrotask(() => totpInputRef?.focus())
    }
  })

  const [loading, data] = useLoading(
    async (): Promise<Resp<{ token: string }>> => {
      return r.post("/auth/login/hash", {
        username: username(),
        password: hashPwd(password()),
        otp_code: opt(),
      })
    },
  )
  const { searchParams, to } = useRouter()

  const Login = async () => {
    if (loading()) return

    const resp = await data()
    handleRespWithoutAuthAndNotify(
      resp,
      (data) => {
        notify.success(t("login.success"))
        changeToken(data.token)
        to(
          decodeURIComponent(searchParams.redirect || base_path || "/"),
          true,
        )
      },
      (msg, code) => {
        if (!needOpt() && code === 402) {
          setOpt("")
          setNeedOpt(true)
        } else {
          notify.error(msg)
          if (needOpt() && code === 402) {
            setOpt("")
            queueMicrotask(() => totpInputRef?.focus())
          }
        }
      },
    )
  }

  return (
    <Center zIndex="$docked" w="$full" h="100vh">
      <VStack
        as="form"
        onSubmit={(e: Event) => {
          e.preventDefault()
          void Login()
        }}
        bgColor={bgColor()}
        rounded="$xl"
        p="24px"
        w={{
          "@initial": "90%",
          "@sm": "364px",
        }}
        spacing="$4"
      >
        <Flex alignItems="center" justifyContent="space-around">
          <Image mr="$2" boxSize="$12" src={logo()} />
          <Heading color="$info9" fontSize="$2xl">
            {title()}
          </Heading>
        </Flex>
        <Show
          when={!needOpt()}
          fallback={
            <Input
              id="totp"
              name="otp"
              autocomplete="one-time-code"
              inputMode="numeric"
              ref={totpInputRef}
              placeholder={t("login.otp-tips")}
              value={opt()}
              onInput={(e) => {
                const code = e.currentTarget.value
                setOpt(code)
                if (/^\d{6}$/.test(code)) {
                  void Login()
                }
              }}
            />
          }
        >
          <Input
            name="username"
            autocomplete="username"
            placeholder={t("login.username-tips")}
            value={username()}
            onInput={(e) => setUsername(e.currentTarget.value)}
          />
          <Input
            name="password"
            autocomplete="current-password"
            placeholder={t("login.password-tips")}
            type="password"
            value={password()}
            onInput={(e) => setPassword(e.currentTarget.value)}
          />
        </Show>
        <HStack w="$full" spacing="$2">
          <Button
            type="button"
            colorScheme="primary"
            w="$full"
            onClick={() => {
              if (needOpt()) {
                setOpt("")
                setNeedOpt(false)
              } else {
                setUsername("")
                setPassword("")
              }
            }}
          >
            {needOpt() ? t("global.back") : t("login.clear")}
          </Button>
          <Button type="submit" w="$full" loading={loading()}>
            {t("login.login")}
          </Button>
        </HStack>
        <Flex
          mt="$2"
          justifyContent="space-evenly"
          alignItems="center"
          color="$neutral10"
          w="$full"
        >
          <SwitchLanguageWhite />
          <SwitchColorMode />
        </Flex>
      </VStack>
      <LoginBg />
    </Center>
  )
}

export default Login
