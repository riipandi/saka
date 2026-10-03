import { Hr, CodeInline } from 'react-email'
import { CardFooter, CardHeader, Text } from '../components'
import { sharedPreviewProps, sharedTemplateProps } from '../constants'
import { BaseTemplate } from '../layouts'

interface OAuthSignInCodeData {
  providerName: string
  code: string
  expirationString: string
}

interface OAuthSignInCodeEmailProps {
  logoURL: string
  appName: string
  data: OAuthSignInCodeData
}

export const OAuthSignInCodeEmail = ({ logoURL, appName, data }: OAuthSignInCodeEmailProps) => {
  return (
    <BaseTemplate logoURL={logoURL} appName={appName}>
      <CardHeader title='Finish Signing In' />
      <Hr style={{ marginTop: '16px' }} />

      <Text style={{ marginTop: '18px' }}>
        A sign-in with {data.providerName} named this email address. Enter this one-time code on the
        sign-in screen to prove it is yours and continue.
      </Text>

      <Text style={{ textAlign: 'center' }}>
        <CodeInline style={{ fontSize: '24px', letterSpacing: '4px' }}>{data.code}</CodeInline>
      </Text>

      <Hr style={{ marginTop: '24px' }} />

      <Text style={{ marginTop: '24px' }}>
        <strong>Important:</strong> This code will expire in {data.expirationString}.
      </Text>

      <Text>
        If you did not make this request, no action is needed — the sign-in cannot continue without
        this code.
      </Text>

      <CardFooter>
        <Text size='sm' style={{ marginBottom: '4px' }}>
          You&apos;re receiving this email because a {appName} sign-in through {data.providerName}{' '}
          carried this address. <br />
          If you are not sure why you&apos;re receiving this, please contact us.
        </Text>
      </CardFooter>
    </BaseTemplate>
  )
}

OAuthSignInCodeEmail.TemplateProps = {
  ...sharedTemplateProps,
  data: {
    providerName: '{{.Data.ProviderName}}',
    code: '{{.Data.Code}}',
    expirationString: '{{.Data.ExpirationString}}'
  }
}

OAuthSignInCodeEmail.PreviewProps = {
  ...sharedPreviewProps,
  data: {
    providerName: 'Hogwarts',
    code: 'EXPECTOPATRONUM',
    expirationString: '15 minutes'
  }
}

export default OAuthSignInCodeEmail
