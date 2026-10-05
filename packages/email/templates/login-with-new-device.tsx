import type { CSSProperties } from 'react'
import { Column, Heading, Hr, Row } from 'react-email'
import { CardFooter, CardHeader, Text } from '../components'
import { sharedPreviewProps, sharedTemplateProps } from '../constants'
import { BaseTemplate } from '../layouts'

interface SignInData {
  location: string
  ipAddress: string
  device: string
  dateTime: string
}

interface NewSignInEmailProps {
  logoURL: string
  appName: string
  data: SignInData
}

export const NewSignInEmail = ({ logoURL, appName, data }: NewSignInEmailProps) => {
  const detailsBoxStyle = { width: '225px', padding: '12px' } satisfies CSSProperties
  const detailsLabelStyle = { margin: 0, color: '#747474' } satisfies CSSProperties
  const detailsBoxValueStyle = { margin: 0, fontSize: '14px' } satisfies CSSProperties

  return (
    <BaseTemplate logoURL={logoURL} appName={appName}>
      <CardHeader title='New Sign-In Detected' warning />
      <Hr style={{ marginTop: '16px' }} />

      <Text>
        Your {appName} account was recently accessed from a new IP address or browser. If you
        recognize this activity, no further action is required.
      </Text>

      <Heading as='h4' style={{ fontSize: '1rem', fontWeight: 'bold', margin: '10px 0' }}>
        Details
      </Heading>

      <Row style={{ backgroundColor: '#f6f6f6', borderRadius: '12px 12px 0 0' }}>
        <Column style={detailsBoxStyle}>
          <Text size='sm' style={detailsLabelStyle}>
            Location
          </Text>
          <Text style={detailsBoxValueStyle}>{data.location}</Text>
        </Column>
        <Column style={detailsBoxStyle}>
          <Text size='sm' style={detailsLabelStyle}>
            IP Address
          </Text>
          <Text style={detailsBoxValueStyle}>{data.ipAddress}</Text>
        </Column>
      </Row>

      <Row style={{ backgroundColor: '#f6f6f6', borderRadius: '0 0 12px 12px', marginTop: '4px' }}>
        <Column style={detailsBoxStyle}>
          <Text size='sm' style={detailsLabelStyle}>
            Device
          </Text>
          <Text style={detailsBoxValueStyle}>{data.device}</Text>
        </Column>
        <Column style={detailsBoxStyle}>
          <Text size='sm' style={detailsLabelStyle}>
            Timestamp
          </Text>
          <Text style={detailsBoxValueStyle}>{data.dateTime}</Text>
        </Column>
      </Row>

      <CardFooter>
        <Text size='sm' style={{ marginBottom: '4px' }}>
          You&apos;re receiving this email because you have an account in {appName}. <br />
          If you are not sure why you&apos;re receiving this, please contact us.
        </Text>
      </CardFooter>
    </BaseTemplate>
  )
}

NewSignInEmail.TemplateProps = {
  ...sharedTemplateProps,
  data: {
    location:
      '{{if and .Data.City .Data.Country}}{{.Data.City}}, {{.Data.Country}}{{else if .Data.Country}}{{.Data.Country}}{{else}}Unknown{{end}}',
    ipAddress: '{{.Data.IPAddress}}',
    device: '{{.Data.Device}}',
    dateTime: '{{.Data.DateTime.Format "January 2, 2006 at 3:04 PM MST"}}'
  }
}

NewSignInEmail.PreviewProps = {
  ...sharedPreviewProps,
  data: {
    location: 'San Francisco, USA',
    ipAddress: '127.0.0.1',
    device: 'Chrome on macOS',
    dateTime: '2024-01-01 12:00 PM UTC'
  }
}

export default NewSignInEmail
